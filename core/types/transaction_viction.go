// Copyright 2026 The Vic-geth Authors
// Viction-specific transaction type helpers.
package types

import (
	"bytes"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/params"
)

// signMethodSelector is the 4-byte function selector for sign(uint256,bytes32).
var signMethodSelector = common.Hex2Bytes("e341eaa4")

// IsSigningTransaction returns true if the transaction is a block-signer
// registration transaction to the BlockSigner contract (0x89).
func (tx *Transaction) IsSigningTransaction() bool {
	if tx.To() == nil {
		return false
	}
	if *tx.To() != params.VictionValidatorBlockSignAddress {
		return false
	}
	data := tx.Data()
	if len(data) < 4 {
		return false
	}
	if !bytes.Equal(data[0:4], signMethodSelector) {
		return false
	}
	// sign(uint256 blockNumber, bytes32 blockHash) = 4 + 32 + 32 = 68 bytes
	if len(data) != 68 {
		return false
	}
	return true
}
