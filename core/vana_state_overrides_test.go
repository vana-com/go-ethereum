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
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

func TestVanaCanonicalStateOverridesTable(t *testing.T) {
	// The totals are the reconciled end-to-end deficits of the two accounts at
	// block 11,037,774; the per-block entries must add up to them exactly.
	want := map[common.Address]*uint256.Int{
		vanaWithdrawalAddrA: gwei(1_306_178_120),
		vanaWithdrawalAddrB: gwei(18_691_466),
	}
	got := map[common.Address]*uint256.Int{}
	seen := map[uint64]bool{}
	for hash, entry := range vanaCanonicalStateOverrides {
		if seen[entry.number] {
			t.Fatalf("block %d registered twice", entry.number)
		}
		seen[entry.number] = true
		if len(entry.overrides) == 0 {
			t.Fatalf("block %d (%x) has no overrides", entry.number, hash)
		}
		for _, o := range entry.overrides {
			if o.sub.IsZero() {
				t.Fatalf("block %d: zero override for %x", entry.number, o.addr)
			}
			if got[o.addr] == nil {
				got[o.addr] = new(uint256.Int)
			}
			got[o.addr].Add(got[o.addr], o.sub)
		}
	}
	if len(vanaCanonicalStateOverrides) != 7 {
		t.Fatalf("expected 7 overridden blocks, have %d", len(vanaCanonicalStateOverrides))
	}
	for addr, sum := range want {
		if got[addr] == nil || got[addr].Cmp(sum) != 0 {
			t.Errorf("address %x: sum of overrides %v, want %v", addr, got[addr], sum)
		}
	}
	if len(got) != len(want) {
		t.Errorf("overrides touch %d accounts, want %d", len(got), len(want))
	}
}

func TestApplyVanaCanonicalStateOverrides(t *testing.T) {
	var (
		// Block 7016957: parent 0x3c795db6…7ca3, timestamp 1770635137.
		parent = common.HexToHash("0x3c795db630c90c64e121bb971a355cf05cb0f23a3b474a65f1540cc2ac307ca3")
		header = &types.Header{ParentHash: parent, Number: big.NewInt(7016957), Time: 1770635137}
		startA = gwei(1_000_000_000_000) // 1000 VANA
		startB = gwei(500_000_000_000)
	)
	withHeader := func(mod func(h *types.Header)) *types.Header {
		h := types.CopyHeader(header)
		mod(h)
		return h
	}
	newState := func() *state.StateDB {
		sdb, _ := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
		sdb.AddBalance(vanaWithdrawalAddrA, startA, tracing.BalanceChangeUnspecified)
		sdb.AddBalance(vanaWithdrawalAddrB, startB, tracing.BalanceChangeUnspecified)
		return sdb
	}
	vana := &params.ChainConfig{ChainID: big.NewInt(1480)}

	// Happy path: both corrections applied.
	sdb := newState()
	applyVanaCanonicalStateOverrides(vana, header, sdb)
	if got, want := sdb.GetBalance(vanaWithdrawalAddrA), new(uint256.Int).Sub(startA, gwei(512_816_693)); got.Cmp(want) != 0 {
		t.Errorf("A: have %v, want %v", got, want)
	}
	if got, want := sdb.GetBalance(vanaWithdrawalAddrB), new(uint256.Int).Sub(startB, gwei(18_691_466)); got.Cmp(want) != 0 {
		t.Errorf("B: have %v, want %v", got, want)
	}

	// Other chain id: untouched.
	sdb = newState()
	applyVanaCanonicalStateOverrides(params.MainnetChainConfig, header, sdb)
	if sdb.GetBalance(vanaWithdrawalAddrA).Cmp(startA) != 0 || sdb.GetBalance(vanaWithdrawalAddrB).Cmp(startB) != 0 {
		t.Errorf("override applied on a non-Vana chain")
	}

	// Unknown parent: untouched.
	sdb = newState()
	applyVanaCanonicalStateOverrides(vana, withHeader(func(h *types.Header) { h.ParentHash = common.HexToHash("0x01") }), sdb)
	if sdb.GetBalance(vanaWithdrawalAddrA).Cmp(startA) != 0 {
		t.Errorf("override applied for an unknown parent hash")
	}

	// Same parent, different timestamp (the orphaned sibling at the previous
	// slot): untouched.
	sdb = newState()
	applyVanaCanonicalStateOverrides(vana, withHeader(func(h *types.Header) { h.Time -= 6 }), sdb)
	if sdb.GetBalance(vanaWithdrawalAddrA).Cmp(startA) != 0 {
		t.Errorf("override applied to a sibling block with a different timestamp")
	}

	// Number mismatch: refused.
	sdb = newState()
	applyVanaCanonicalStateOverrides(vana, withHeader(func(h *types.Header) { h.Number = big.NewInt(7016958) }), sdb)
	if sdb.GetBalance(vanaWithdrawalAddrA).Cmp(startA) != 0 {
		t.Errorf("override applied despite block number mismatch")
	}

	// Insufficient balance: refused, nothing partially applied.
	sdb, _ = state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	sdb.AddBalance(vanaWithdrawalAddrA, gwei(1), tracing.BalanceChangeUnspecified)
	applyVanaCanonicalStateOverrides(vana, header, sdb)
	if sdb.GetBalance(vanaWithdrawalAddrA).Cmp(gwei(1)) != 0 {
		t.Errorf("override applied despite insufficient balance")
	}
}
