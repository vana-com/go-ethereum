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

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// Vana mainnet (chain id 1480) canonical-state overrides.
//
// Seven canonical Vana mainnet blocks were sealed by proposers running a
// go-ethereum version affected by the path-scheme duplicate-root layer bug
// (fixed upstream in ethereum/go-ethereum#34642, first released in v1.17.3).
// Each of those blocks is the first canonical block after a reorg. The
// proposer read the balance of the withdrawal/fee-recipient account(s) touched
// by the orphaned sibling block from a stale layer, so its post-state misses the
// withdrawal credits those accounts received in the canonical blocks between the
// fork point and the bad block. The resulting state root is canonical and is
// what every node must reproduce, so a correct re-execution has to subtract the
// same missing credit after applying the block's own transactions and
// withdrawals.
//
// The overrides are keyed by parent hash, block number and timestamp and only
// apply on chain id 1480, so they are inert for every other network and for
// any side-chain block at the same height. Nodes that joined through a post-July-2026 snapshot or a snap
// sync never execute these blocks and are unaffected.
//
// The missing credits were established by reconciling the two accounts'
// balances (genesis allocation + withdrawals + proposer priority fees + own
// transactions) against an archive node at every block, and each shortfall
// decomposes exactly into the withdrawals of the dropped canonical blocks.
// The override at 9228152 is the one that ran in production as
// geth-v1.16.8-vana9228152.
var (
	vanaMainnetChainID = big.NewInt(1480)

	// vanaWithdrawalAddrA is the withdrawal address of 30 of the 31 validators
	// and the fee recipient of the vast majority of blocks.
	vanaWithdrawalAddrA = common.HexToAddress("0x7492933BB94F79df306FeB86A4ed1927a0a51B31")
	// vanaWithdrawalAddrB is the withdrawal address of the remaining validator.
	vanaWithdrawalAddrB = common.HexToAddress("0x764d0FEb920f1a2000f9eAF390c7ec80D157b576")
)

// vanaBalanceOverride is a balance correction to apply after a block has been
// fully executed (transactions, system calls and withdrawals included).
type vanaBalanceOverride struct {
	addr common.Address
	sub  *uint256.Int // amount to subtract, in wei
}

// vanaCanonicalStateOverride describes the corrections for one canonical block.
// The block is identified by its parent hash (the map key), number and
// timestamp: those survive stateless execution, where the block's own hash is
// not known yet because the state and receipt roots are blank, and together
// they still exclude the orphaned sibling, which was proposed at an earlier
// slot and therefore carries a different timestamp.
type vanaCanonicalStateOverride struct {
	number    uint64
	time      uint64
	hash      common.Hash // canonical block hash, for logging and documentation
	root      common.Hash // canonical state root, for logging and documentation
	overrides []vanaBalanceOverride
}

// gwei converts a gwei amount (the unit of beacon-chain withdrawals) to wei.
func gwei(amount uint64) *uint256.Int {
	return new(uint256.Int).Mul(uint256.NewInt(amount), uint256.NewInt(params.GWei))
}

// vanaCanonicalStateOverrides maps the parent hash of each affected canonical
// block to the corrections that reproduce its canonical state root.
var vanaCanonicalStateOverrides = map[common.Hash]vanaCanonicalStateOverride{
	// 2026-02-09 11:05:37 UTC.
	// Missing: withdrawals of blocks 7016952-7016956 (A) and 7016954 (B).
	common.HexToHash("0x3c795db630c90c64e121bb971a355cf05cb0f23a3b474a65f1540cc2ac307ca3"): {
		number: 7016957,
		time:   1770635137,
		hash:   common.HexToHash("0x230da516490050f8140d6e0ef787f23633c39b36480b08f37417a173a2753984"),
		root:   common.HexToHash("0x507e8bdb30c181e6c07a433cf00efd75a00cef7c79cd9fef1ba57505f1e21058"),
		overrides: []vanaBalanceOverride{
			{vanaWithdrawalAddrA, gwei(512_816_693)},
			{vanaWithdrawalAddrB, gwei(18_691_466)},
		},
	},
	// 2026-02-27 06:10:25 UTC.
	// Missing: withdrawals of blocks 7267265-7267266.
	common.HexToHash("0xa808d5c3ddd398b1879f4c6a4460c3e7ba68ac7503d68365fa9fb5964e11da62"): {
		number:    7267267,
		time:      1772172625,
		hash:      common.HexToHash("0xeb77a2f9f8a9d13122b26de41931b87db13d2ad0dd95a776ca9d6865d0044714"),
		root:      common.HexToHash("0x9e6fcc58e6e387e0b25c9b482e7e341403f08d45ab678701f693777d0b2e0eec"),
		overrides: []vanaBalanceOverride{{vanaWithdrawalAddrA, gwei(198_033_729)}},
	},
	// 2026-03-01 01:01:43 UTC.
	// Missing: withdrawals of block 7292192.
	common.HexToHash("0xb39f8b0f7d097c7401695d649c8d01a75a0c09f78446901937c943de3b3c2ce1"): {
		number:    7292193,
		time:      1772326903,
		hash:      common.HexToHash("0x4512385e5da22414a74d7e766e27dd651acb3f80ef9fcbab83a7b925fbfd5dc5"),
		root:      common.HexToHash("0x252019ecfe983289545d5b901ccc99f18ce105ac49aab7c198de85e7e9fc39cf"),
		overrides: []vanaBalanceOverride{{vanaWithdrawalAddrA, gwei(21_012)}},
	},
	// 2026-06-04 07:38:37 UTC.
	// Missing: withdrawals of block 8662557.
	common.HexToHash("0x8cc4643f8df3dd6af563da55318c7163df734ba9aeeddce4fe2d8b71dcd90364"): {
		number:    8662558,
		time:      1780558717,
		hash:      common.HexToHash("0x2552abb58ba8b642f91aba471cb8c0b1ee3dafd743dbccff9530a0e0583a3faf"),
		root:      common.HexToHash("0x5b950fc0515ecfd8c5f754e7ecac01077ebea3ed1d42d0d327260680a3aba9e1"),
		overrides: []vanaBalanceOverride{{vanaWithdrawalAddrA, gwei(11_142_468)}},
	},
	// 2026-06-05 14:20:55 UTC.
	// Missing: withdrawals of block 8680690.
	common.HexToHash("0xa62b4e971498a0def1ae58f919c605199a26128c54b23fd715933b28f6e4848e"): {
		number:    8680691,
		time:      1780669255,
		hash:      common.HexToHash("0x90f0fed601f88550b3c847270e3a8cd232b5f3d448d3aba3336ca6a5becc11d2"),
		root:      common.HexToHash("0x8a7fd2bd28d0c206a7da7de948d5b08a04fbedb6d74f72681ed7c38931442ea0"),
		overrides: []vanaBalanceOverride{{vanaWithdrawalAddrA, gwei(8_280_437)}},
	},
	// 2026-06-21 09:23:55 UTC.
	// Missing: withdrawals of block 8907947.
	common.HexToHash("0xc8230bac27ddb61c4680860e393b27fc99b6ea807af20ee527fd74b3fdff0f5b"): {
		number:    8907948,
		time:      1782033835,
		hash:      common.HexToHash("0xd434a095e9a3f7a3a73b2da87730262d05083dfb8e1fa34459ad9f194283e720"),
		root:      common.HexToHash("0x18658aeeea8397ea30adcdd053e4df41083024473bf27dea5280c4eb2abc3734"),
		overrides: []vanaBalanceOverride{{vanaWithdrawalAddrA, gwei(286_699_567)}},
	},
	// 2026-07-13 17:51:49 UTC.
	// Missing: withdrawals of block 9228151. Previously shipped as geth-v1.16.8-vana9228152.
	common.HexToHash("0xce31b2135c3fa49e638913277dc2eb8a10fe8af6b633dbb409f92458d8dfd960"): {
		number:    9228152,
		time:      1783965109,
		hash:      common.HexToHash("0xf83f4c002aa9d442e5b340621dd1cee26a2e2453e271d43e67999219c4c5fe82"),
		root:      common.HexToHash("0x7b460734cec6f4024163fd0a064448d2fab617c2859deeb1767cec47a4d2def6"),
		overrides: []vanaBalanceOverride{{vanaWithdrawalAddrA, gwei(289_184_214)}},
	},
}

// applyVanaCanonicalStateOverrides applies the balance corrections registered
// for the block with the given header, if any. It must be called after the block
// has been fully executed and finalized (withdrawals credited) and before the
// state root is computed. It is a no-op on every chain other than Vana mainnet.
func applyVanaCanonicalStateOverrides(config *params.ChainConfig, header *types.Header, statedb vm.StateDB) {
	if config == nil || config.ChainID == nil || config.ChainID.Cmp(vanaMainnetChainID) != 0 {
		return
	}
	entry, ok := vanaCanonicalStateOverrides[header.ParentHash]
	if !ok {
		return
	}
	number := header.Number.Uint64()
	if entry.number != number || entry.time != header.Time {
		// Same parent but a different slot: this is the orphaned sibling (or
		// some other side-chain block), which was sealed from a correct state.
		log.Warn("Vana canonical state override skipped for side-chain block", "number", number, "time", header.Time, "parent", header.ParentHash, "want", entry.number, "wantTime", entry.time)
		return
	}
	for _, o := range entry.overrides {
		prev := statedb.GetBalance(o.addr)
		if prev.Lt(o.sub) {
			log.Error("Vana canonical state override exceeds balance", "number", number, "hash", entry.hash, "address", o.addr, "balance", prev, "sub", o.sub)
			return
		}
		statedb.SubBalance(o.addr, o.sub, tracing.BalanceChangeUnspecified)
		log.Info("Applied Vana canonical state override", "number", number, "hash", entry.hash, "root", entry.root, "address", o.addr, "sub", o.sub, "balance", statedb.GetBalance(o.addr))
	}
}
