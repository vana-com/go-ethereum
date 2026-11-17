// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/stateless"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
)

// vanaReplayInput is one bad block plus everything needed to execute it
// statelessly: the block (RPC JSON with full transactions and withdrawals),
// its ancestor headers (parent first), the bytecodes and the MPT nodes that
// execution touches, all taken from an archive node at the parent block with
// eth_getProof / eth_getCode / the prestate tracer.
type vanaReplayInput struct {
	Number  uint64          `json:"number"`
	Block   json.RawMessage `json:"block"`
	Headers []*types.Header `json:"headers"`
	Codes   []hexutil.Bytes `json:"codes"`
	Nodes   []hexutil.Bytes `json:"nodes"`
}

type vanaReplayBody struct {
	Hash         common.Hash          `json:"hash"`
	Transactions []*types.Transaction `json:"transactions"`
	Withdrawals  []*types.Withdrawal  `json:"withdrawals"`
}

// TestVanaStatelessReplay re-executes each of the seven stale-sealed Vana
// mainnet blocks on top of the real pre-state (a witness built from an archive
// node) through the regular StateProcessor.Process path and checks that the
// resulting state root equals the canonical one with the overrides, and does
// not without them.
//
// The inputs in testdata/vana were captured from a Vana archive node (Geth
// v1.17.5, hash scheme, gcmode=archive) on 2026-11-17. VANA_WITNESS_DIR (a
// directory of <number>.json files) and VANA_GENESIS_JSON (the Vana mainnet
// genesis file, for the chain config) override them.
func TestVanaStatelessReplay(t *testing.T) {
	dir, genesisPath := os.Getenv("VANA_WITNESS_DIR"), os.Getenv("VANA_GENESIS_JSON")
	if dir == "" {
		dir = filepath.Join("testdata", "vana", "witness")
	}
	if genesisPath == "" {
		genesisPath = filepath.Join("testdata", "vana", "genesis.json")
	}
	genesisBlob, err := os.ReadFile(genesisPath)
	if err != nil {
		t.Fatal(err)
	}
	var genesis Genesis
	if err := json.Unmarshal(genesisBlob, &genesis); err != nil {
		t.Fatal(err)
	}
	config := genesis.Config
	if config.ChainID.Cmp(vanaMainnetChainID) != 0 {
		t.Fatalf("genesis chain id %v is not Vana mainnet", config.ChainID)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no witness files in %s: %v", dir, err)
	}
	replayed := map[uint64]bool{}
	for _, file := range files {
		blob, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var in vanaReplayInput
		if err := json.Unmarshal(blob, &in); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var header types.Header
		if err := json.Unmarshal(in.Block, &header); err != nil {
			t.Fatalf("%s: header: %v", file, err)
		}
		var body vanaReplayBody
		if err := json.Unmarshal(in.Block, &body); err != nil {
			t.Fatalf("%s: body: %v", file, err)
		}
		if header.Hash() != body.Hash {
			t.Fatalf("%s: header re-hashes to %x, RPC says %x", file, header.Hash(), body.Hash)
		}
		entry, ok := vanaCanonicalStateOverrides[header.ParentHash]
		if !ok || entry.number != header.Number.Uint64() || entry.time != header.Time {
			t.Fatalf("%s: block %d (%x) is not in the override table", file, header.Number, header.Hash())
		}
		if entry.hash != header.Hash() || entry.root != header.Root {
			t.Fatalf("%s: table documents hash %x root %x, block has %x %x", file, entry.hash, entry.root, header.Hash(), header.Root)
		}
		if len(in.Headers) == 0 || in.Headers[0].Hash() != header.ParentHash {
			t.Fatalf("%s: first witness header is not the parent", file)
		}
		canonicalRoot := header.Root

		// The stateless runner expects the roots it has to compute to be blank.
		gutted := types.CopyHeader(&header)
		gutted.Root, gutted.ReceiptHash = common.Hash{}, common.Hash{}
		block := types.NewBlockWithHeader(gutted).WithBody(types.Body{Transactions: body.Transactions, Withdrawals: body.Withdrawals})

		run := func() (common.Hash, common.Hash, error) {
			witness, err := stateless.NewWitness(gutted, nil, false)
			if err != nil {
				return common.Hash{}, common.Hash{}, err
			}
			witness.Headers = in.Headers
			for _, code := range in.Codes {
				witness.AddCode(code)
			}
			nodes := make(map[string][]byte, len(in.Nodes))
			for i, node := range in.Nodes {
				nodes[string(rune(i))] = node
			}
			witness.AddState(nodes, common.Hash{})
			return ExecuteStateless(context.Background(), config, vm.Config{}, block, witness)
		}

		// With the overrides: must reproduce the canonical root.
		root, receiptRoot, err := run()
		if err != nil {
			t.Fatalf("block %d: stateless execution failed: %v", in.Number, err)
		}
		if receiptRoot != header.ReceiptHash {
			t.Errorf("block %d: receipt root %x, canonical %x", in.Number, receiptRoot, header.ReceiptHash)
		}
		if root != canonicalRoot {
			t.Errorf("block %d: state root with overrides %x, canonical %x", in.Number, root, canonicalRoot)
		} else {
			t.Logf("block %d (%d txs, %d withdrawals): canonical root %x reproduced", in.Number, len(body.Transactions), len(body.Withdrawals), root)
		}

		// Without the overrides (vanilla behaviour): must NOT reproduce it.
		saved := vanaCanonicalStateOverrides
		vanaCanonicalStateOverrides = map[common.Hash]vanaCanonicalStateOverride{}
		vanillaRoot, _, err := run()
		vanaCanonicalStateOverrides = saved
		if err != nil {
			t.Fatalf("block %d: vanilla stateless execution failed: %v", in.Number, err)
		}
		if vanillaRoot == canonicalRoot {
			t.Errorf("block %d: vanilla execution already reproduces the canonical root; override is not needed", in.Number)
		} else {
			t.Logf("block %d: vanilla execution yields %x (differs from canonical, as expected)", in.Number, vanillaRoot)
		}
		replayed[in.Number] = true
	}
	for hash, entry := range vanaCanonicalStateOverrides {
		if !replayed[entry.number] {
			t.Errorf("block %d (parent %x) has no witness file; not replayed", entry.number, hash)
		}
	}
}
