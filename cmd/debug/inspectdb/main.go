// Copyright 2026 The Viction Authors
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

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
)

const targetBlock uint64 = 8_505_900

var (
	targetHash = common.HexToHash("0x784fd3f4c3952989a8f3285a07ed80a972f145771e3870701518638d9b193240")
	parentHash = common.HexToHash("0xc2c73bba7452b00d3d2befb8499567c7e3b7259ae0d57bd77fb423489445c41f")
	parentRoot = common.HexToHash("0x210a371642615fd1b571c2d17dee682bf5aaf0e3f990c30d430bb7a61e807eb0")
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./cmd/debug/inspectdb <chaindata>")
		os.Exit(2)
	}
	path := os.Args[1]
	db, err := rawdb.NewLevelDBDatabaseWithFreezer(path, 128, 64, filepath.Join(path, "ancient"), "inspect", true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open database: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	printHead := func(label string, hash common.Hash) {
		number := rawdb.ReadHeaderNumber(db, hash)
		if number == nil {
			fmt.Printf("%s hash=%s number=<missing>\n", label, hash)
			return
		}
		header := rawdb.ReadHeader(db, hash, *number)
		if header == nil {
			fmt.Printf("%s hash=%s number=%d header=<missing>\n", label, hash, *number)
			return
		}
		fmt.Printf("%s hash=%s number=%d root=%s parent=%s\n", label, hash, *number, header.Root, header.ParentHash)
	}

	printHead("head-header", rawdb.ReadHeadHeaderHash(db))
	printHead("head-block", rawdb.ReadHeadBlockHash(db))
	printHead("head-fast", rawdb.ReadHeadFastBlockHash(db))
	for _, number := range []uint64{targetBlock - 5, targetBlock - 1, targetBlock} {
		printHead(fmt.Sprintf("canonical-%d", number), rawdb.ReadCanonicalHash(db, number))
	}
	printHead("exact-parent", parentHash)
	printHead("exact-target", targetHash)

	if body := rawdb.ReadBody(db, targetHash, targetBlock); body != nil {
		fmt.Printf("exact-target-body txs=%d uncles=%d\n", len(body.Transactions), len(body.Uncles))
	} else {
		fmt.Println("exact-target-body missing")
	}
	if bad := rawdb.ReadBadBlock(db, targetHash); bad != nil {
		fmt.Printf("bad-block hash=%s number=%d txs=%d root=%s parent=%s\n", bad.Hash(), bad.NumberU64(), len(bad.Transactions()), bad.Root(), bad.ParentHash())
	} else {
		fmt.Printf("bad-block hash=%s missing\n", targetHash)
	}

	genesis := rawdb.ReadCanonicalHash(db, 0)
	fmt.Printf("genesis=%s config=%+v\n", genesis, rawdb.ReadChainConfig(db, genesis))
	parentState, err := state.New(parentRoot, state.NewDatabase(db), nil)
	if err != nil {
		fmt.Printf("parent-state root=%s unavailable err=%v\n", parentRoot, err)
		return
	}
	validator := common.HexToAddress("0x0000000000000000000000000000000000000088")
	foundation := common.HexToAddress("0x0000000000000000000000000000000000000068")
	fmt.Printf("parent-state root=%s available validatorSlot0=%s foundationBalance=%s\n", parentRoot, parentState.GetState(validator, common.Hash{}), parentState.GetBalance(foundation))
}
