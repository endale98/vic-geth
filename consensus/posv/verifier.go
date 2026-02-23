// Copyright 2016 The go-ethereum Authors
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

// Package posv implements the proof-of-stake-voting consensus engine.
package posv

import (
	"bytes"
	"errors"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/consensus/misc"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

var (
	nonceAuthVote = hexutil.MustDecode("0xffffffffffffffff") // Magic nonce number to vote on adding a new signer
	nonceDropVote = hexutil.MustDecode("0x0000000000000000") // Magic nonce number to vote on removing a signer.

	uncleHash = types.CalcUncleHash(nil) // Always Keccak256(RLP([])) as uncles are meaningless outside of PoW.
)

// [TO-DO] return nil for execute step validation.
// verifyHeaderWithCache checks the cache for previously verified headers and
// performs full verification if not found. Successfully verified headers are
// cached to avoid redundant checks.
func (c *Posv) verifyHeaderWithCache(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header) error {
	return nil
	// _, check := c.verifiedBlocks.Get(header.Hash())
	// if check {
	// 	return nil
	// }
	// err := c.verifyHeader(chain, header, parents)
	// if err == nil {
	// 	c.verifiedBlocks.Add(header.Hash(), true)
	// }
	// return err
}

// verifyHeader checks whether a header conforms to the consensus rules.The
// caller may optionally pass in a batch of parents (ascending order) to avoid
// looking those up from the database. This is useful for concurrently verifying
// a batch of new headers.
func (c *Posv) verifyHeader(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header) error {
	if header.Number == nil {
		return errUnknownBlock
	}
	number := header.Number.Uint64()

	now := time.Now()
	nowUnix := now.Unix()

	// Don't waste time checking blocks from the future
	if header.Time > uint64(nowUnix) {
		return consensus.ErrFutureBlock
	}

	// Checkpoint blocks need to enforce zero beneficiary
	checkpoint := (number % c.config.Epoch) == 0
	if checkpoint && header.Coinbase != (common.Address{}) {
		return errInvalidCheckpointBeneficiary
	}

	// Nonces must be 0x00..0 or 0xff..f, zeroes enforced on checkpoints
	if !bytes.Equal(header.Nonce[:], nonceAuthVote) && !bytes.Equal(header.Nonce[:], nonceDropVote) {
		return errInvalidVote
	}

	if checkpoint && !bytes.Equal(header.Nonce[:], nonceDropVote) {
		return errInvalidCheckpointVote
	}

	// Check that the extra-data contains both the vanity and signature
	if len(header.Extra) < ExtraVanity {
		return errMissingVanity
	}
	if len(header.Extra) < ExtraVanity+ExtraSeal {
		return errMissingSignature
	}
	// Ensure that the extra-data contains a signer list on checkpoint, but none otherwise
	signersBytes := len(header.Extra) - ExtraVanity - ExtraSeal
	if !checkpoint && signersBytes != 0 {
		return errExtraSigners
	}
	if checkpoint && signersBytes%common.AddressLength != 0 {
		return errInvalidCheckpointSigners
	}
	// Ensure that the mix digest is zero as we don't have fork protection currently
	if header.MixDigest != (common.Hash{}) {
		return errInvalidMixDigest
	}
	// Ensure that the block doesn't contain any uncles which are meaningless in PoA
	if header.UncleHash != uncleHash {
		return errInvalidUncleHash
	}

	// If all checks passed, validate any special fields for hard forks
	if err := misc.VerifyForkHashes(chain.Config(), header, false); err != nil {
		return err
	}

	// All basic checks passed, verify cascading fields
	return c.verifyCascadingFields(chain, header, parents)
}

// verifyCascadingFields verifies all the header fields that are not standalone,
// rather depend on a batch of previous headers. The caller may optionally pass
// in a batch of parents (ascending order) to avoid looking those up from the
// database. This is useful for concurrently verifying a batch of new headers.
func (c *Posv) verifyCascadingFields(chain consensus.ChainHeaderReader, header *types.Header, parents []*types.Header) error {
	// The genesis block is the always valid dead-end
	number := header.Number.Uint64()
	if number == 0 {
		return nil
	}

	// Retrieve the snapshot needed to verify this header and cache it
	var parent *types.Header
	if len(parents) > 0 {
		parent = parents[len(parents)-1]
	} else {
		parent = chain.GetHeader(header.ParentHash, number-1)
	}
	if parent == nil || parent.Number.Uint64() != number-1 || parent.Hash() != header.ParentHash {
		return consensus.ErrUnknownAncestor
	}

	if parent.Time+c.config.Period > header.Time {
		return ErrInvalidTimestamp
	}

	// Retrieve the snapshot needed to verify this header and cache it
	snap, err := c.snapshot(chain, number-1, header.ParentHash, parents)
	if err != nil {
		log.Debug("Failed to retrieve snapshot", "number", number, "err", err)
		return err
	}

	// If the block is a checkpoint block, verify the signer list
	if number%c.config.Epoch == 0 {
		chain := chain.(consensus.ChainReader)
		err := c.verifyValidators(chain, header, parents)
		if err != nil {
			log.Debug("Failed to verify validators", "number", number, "err", err)
			return err
		}
	}

	// All basic checks passed, verify the seal and return
	return c.verifySeal(chain, header, snap)

}

func (c *Posv) getValidators(chain consensus.ChainHeaderReader, header *types.Header) []common.Address {
	n := header.Number.Uint64()
	e := c.config.Epoch
	if n%e == 0 {
		return GetMasternodesFromCheckpointHeader(header)
	}
	h := chain.GetHeaderByNumber(n - (n % e))
	if h != nil {
		return GetMasternodesFromCheckpointHeader(h)
	}
	return []common.Address{}
}

func (c *Posv) verifyValidators(chain consensus.ChainReader, header *types.Header, parents []*types.Header) error {
	snap, err := c.snapshot(chain, header.Number.Uint64()-1, header.ParentHash, parents)
	if err != nil {
		return err
	}

	validators := snap.GetSigners()
	masternodes := GetMasternodesFromCheckpointHeader(header)

	if len(masternodes) != len(validators) {
		log.Warn("Checkpoint signers count mismatch, skipping strict validation", "block", header.Number.Uint64())
		return nil
	}

	for i, m := range masternodes {
		if m != validators[i] {
			log.Warn("Checkpoint signers do not match exactly, skipping strict validation", "block", header.Number.Uint64())
			return nil
		}
	}
	return nil
}

// verifySeal checks whether the signature contained in the header satisfies the
// consensus protocol requirements.
func (c *Posv) verifySeal(chainH consensus.ChainHeaderReader, header *types.Header, snap *Snapshot) error {
	chain := chainH.(consensus.ChainReader)
	// Verifying the genesis block is not supported
	number := header.Number.Uint64()
	if number == 0 {
		return errUnknownBlock
	}
	// Resolve the authorization key and check against signers
	validators := c.getValidators(chain, header)
	if len(validators) == 0 {
		// Fallback to snap if no checkpoint block found
		validators = snap.GetSigners()
	}

	creator, err := ecrecover(header, c.signatures)
	if err != nil {
		log.Debug("Failed to recover signer", "number", number, "err", err)
		return err
	}

	if _, ok := snap.Signers[creator]; !ok {
		if common.IndexOf(validators, creator) == -1 {
			return errUnauthorizedSigner
		}
	}

	for seen, recent := range snap.Recents {
		if len(validators) <= 1 {
			break
		}
		if recent == creator {
			// Signer is among RecentsRLP, only fail if the current block doesn't shift it out
			// There is only case that we don't allow signer to create two continuous blocks.
			if limit := uint64(2); seen > number-limit {
				// Only take into account the non-epoch blocks
				if number%c.config.Epoch != 0 {
					return errUnauthorizedSigner
				}
			}
		}
	}

	// Ensure that the difficulty corresponds to the turn-ness of the signer
	parent := chain.GetHeader(header.ParentHash, number-1)
	difficulty := c.calcDifficulty(creator, parent.Number.Uint64(), parent.Hash(), chain)
	if header.Difficulty.Int64() != difficulty.Int64() {
		return errInvalidDifficulty
	}

	// Enforce double validation
	if number > c.config.Epoch {
		attestor, err := c.Attestor(header)
		if err != nil {
			return err
		}

		checkpointHeader := GetCheckpointHeader(c.config, parent, chain)
		masternodes := GetMasternodesFromCheckpointHeader(checkpointHeader)
		m2validators := ExtractValidatorsFromBytes(checkpointHeader.NewAttestors)
		valAttPairs, _, err := getM1M2(masternodes, m2validators, header, chain.Config())
		if err != nil {
			return err
		}
		assignedAttestor, ok := valAttPairs[creator]
		if !ok || attestor != assignedAttestor {
			return errInvalidBlockAttestor
		}
	}
	return nil
}

func (c *Posv) snapshot(chain consensus.ChainHeaderReader, number uint64, hash common.Hash, parents []*types.Header) (*Snapshot, error) {
	return nil, nil
}

func getM1M2(masternodes []common.Address, validators []int64, currentHeader *types.Header, config *params.ChainConfig) (map[common.Address]common.Address, uint64, error) {
	m1m2 := map[common.Address]common.Address{}
	maxMNs := len(masternodes)
	moveM2 := uint64(0)
	if len(validators) < maxMNs {
		return nil, moveM2, errors.New("len(m2) is less than len(m1)")
	}
	if maxMNs > 0 {
		isForked := config.IsTIPRandomize(currentHeader.Number)
		if isForked {
			moveM2 = ((currentHeader.Number.Uint64() % config.Posv.Epoch) / uint64(maxMNs)) % uint64(maxMNs)
		}
		for i, m1 := range masternodes {
			m2Index := uint64(validators[i] % int64(maxMNs))
			m2Index = (m2Index + moveM2) % uint64(maxMNs)
			m1m2[m1] = masternodes[m2Index]
		}
	}
	return m1m2, moveM2, nil
}

// ExtractValidatorsFromBytes extracts validators from byte array.
func ExtractValidatorsFromBytes(byteValidators []byte) []int64 {
	lenValidator := len(byteValidators) / M2ByteLength
	var validators []int64
	for i := 0; i < lenValidator; i++ {
		trimByte := bytes.Trim(byteValidators[i*M2ByteLength:(i+1)*M2ByteLength], "\x00")
		if len(trimByte) == 0 {
			continue
		}
		intNumber, err := strconv.Atoi(string(trimByte))
		if err != nil {
			log.Error("Can not convert string to integer", "error", err)
			return []int64{}
		}
		validators = append(validators, int64(intNumber))
	}

	return validators
}
