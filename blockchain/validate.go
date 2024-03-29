// Copyright (c) 2013-2017 The btcsuite developers
// Use of this source code is governed by an ISC
// license that can be found in the LICENSE file.

package blockchain

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/bourbaki-czz/classzz/chaincfg"
	"github.com/bourbaki-czz/classzz/chaincfg/chainhash"
	"github.com/bourbaki-czz/classzz/consensus"
	"github.com/bourbaki-czz/classzz/cross"
	"github.com/bourbaki-czz/classzz/txscript"
	"github.com/bourbaki-czz/classzz/wire"
	"github.com/bourbaki-czz/czzutil"
)

const (
	// semantic helpers
	oneMegabyte = 1000000

	// MaxTimeOffsetSeconds is the maximum number of seconds a block time
	// is allowed to be ahead of the current time.  This is currently 2
	// hours.
	MaxTimeOffsetSeconds = 2 * 60 * 60

	// MinCoinbaseScriptLen is the minimum length a coinbase script can be.
	MinCoinbaseScriptLen = 2

	// MaxCoinbaseScriptLen is the maximum length a coinbase script can be.
	MaxCoinbaseScriptLen = 100

	// medianTimeBlocks is the number of previous blocks which should be
	// used to calculate the median time used to validate block timestamps.
	medianTimeBlocks = 11

	// baseSubsidy is the starting subsidy amount for mined blocks.  This
	// value is halved every SubsidyHalvingInterval blocks.
	baseSubsidy = 1000 * czzutil.SatoshiPerBitcoin

	// LegacyMaxBlockSize is the maximum number of bytes allowed in a block
	// prior to the August 1st, 2018 UAHF hardfork
	LegacyMaxBlockSize = 1000000

	// MaxBlockSigOpsPerMB is the maximum number of allowed sigops allowed
	// per one (or partial) megabyte of block size after the UAHF hard fork
	MaxBlockSigOpsPerMB = 20000

	// MaxTransactionSize is the maximum allowable size of a transaction
	// after the UAHF hard fork
	MaxTransactionSize = oneMegabyte

	// MinTransactionSize is the minimum transaction size allowed on the
	// network after the magneticanomaly hardfork
	MinTransactionSize = 100

	// MaxTransactionSigOps is the maximum allowable number of sigops per
	// transaction after the UAHF hard fork
	MaxTransactionSigOps = 20000

	allowedFutureBlockTime = 10 * time.Second
)

var (
	// zeroHash is the zero value for a chainhash.Hash and is defined as
	// a package level variable to avoid the need to create a new instance
	// every time a check is needed.
	zeroHash chainhash.Hash
)

// MaxBlockSize returns the is the maximum number of bytes allowed in
// a block.
func (b *BlockChain) MaxBlockSize() int {
	return int(b.excessiveBlockSize)
}

// MaxBlockSigOps returns the maximum allowable number of signature
// operations in a block. The value is a function of the serialized
// block size in bytes.
func MaxBlockSigOps(nBlockBytes uint32) int {
	nBlockMBytesRoundedUp := 1 + ((int(nBlockBytes) - 1) / oneMegabyte)
	return nBlockMBytesRoundedUp * MaxBlockSigOpsPerMB
}

// isNullOutpoint determines whether or not a previous transaction output point
// is set.
func isNullOutpoint(outpoint *wire.OutPoint) bool {
	if outpoint.Index == math.MaxUint32 && outpoint.Hash == zeroHash {
		return true
	}
	return false
}

// IsCoinBaseTx determines whether or not a transaction is a coinbase.  A coinbase
// is a special transaction created by miners that has no inputs.  This is
// represented in the block chain by a transaction with a single input that has
// a previous output transaction index set to the maximum value along with a
// zero hash.
//
// This function only differs from IsCoinBase in that it works with a raw wire
// transaction as opposed to a higher level util transaction.
func IsCoinBaseTx(msgTx *wire.MsgTx) bool {
	// A coin base must only have one transaction input.
	height, err := ExtractCoinbaseHeight(czzutil.NewTx(msgTx))
	if err != nil {
		return false
	}

	if height >= chaincfg.MainNetParams.EntangleHeight {
		if len(msgTx.TxIn) != 3 {
			return false
		}
	} else {
		if len(msgTx.TxIn) != 1 {
			return false
		}
	}

	// The previous output of a coin base must have a max value index and
	// a zero hash.
	prevOut := &msgTx.TxIn[0].PreviousOutPoint
	if prevOut.Index != math.MaxUint32 || prevOut.Hash != zeroHash {
		return false
	}

	return true
}
func isCoinBaseInParam(tx *czzutil.Tx, chainParams *chaincfg.Params) bool {
	msgTx := tx.MsgTx()
	height, err := ExtractCoinbaseHeight(czzutil.NewTx(msgTx))
	if err != nil {
		return false
	}

	if height >= chainParams.EntangleHeight {
		if len(msgTx.TxIn) != 3 {
			return false
		}
	} else {
		if len(msgTx.TxIn) != 1 {
			return false
		}
	}

	// The previous output of a coin base must have a max value index and
	// a zero hash.
	prevOut := &msgTx.TxIn[0].PreviousOutPoint
	if prevOut.Index != math.MaxUint32 || prevOut.Hash != zeroHash {
		return false
	}

	return true
}

// IsCoinBase determines whether or not a transaction is a coinbase.  A coinbase
// is a special transaction created by miners that has no inputs.  This is
// represented in the block chain by a transaction with a single input that has
// a previous output transaction index set to the maximum value along with a
// zero hash.
//
// This function only differs from IsCoinBaseTx in that it works with a higher
// level util transaction as opposed to a raw wire transaction.
func IsCoinBase(tx *czzutil.Tx) bool {
	return IsCoinBaseTx(tx.MsgTx())
}

// SequenceLockActive determines if a transaction's sequence locks have been
// met, meaning that all the inputs of a given transaction have reached a
// height or time sufficient for their relative lock-time maturity.
func SequenceLockActive(sequenceLock *SequenceLock, blockHeight int32,
	medianTimePast time.Time) bool {

	// If either the seconds, or height relative-lock time has not yet
	// reached, then the transaction is not yet mature according to its
	// sequence locks.
	if sequenceLock.Seconds >= medianTimePast.Unix() ||
		sequenceLock.BlockHeight >= blockHeight {
		return false
	}

	return true
}

// IsFinalizedTransaction determines whether or not a transaction is finalized.
func IsFinalizedTransaction(tx *czzutil.Tx, blockHeight int32, blockTime time.Time) bool {
	msgTx := tx.MsgTx()

	// Lock time of zero means the transaction is finalized.
	lockTime := msgTx.LockTime
	if lockTime == 0 {
		return true
	}

	// The lock time field of a transaction is either a block height at
	// which the transaction is finalized or a timestamp depending on if the
	// value is before the txscript.LockTimeThreshold.  When it is under the
	// threshold it is a block height.
	var blockTimeOrHeight int64
	if lockTime < txscript.LockTimeThreshold {
		blockTimeOrHeight = int64(blockHeight)
	} else {
		blockTimeOrHeight = blockTime.Unix()
	}
	if int64(lockTime) < blockTimeOrHeight {
		return true
	}

	// At this point, the transaction's lock time hasn't occurred yet, but
	// the transaction might still be finalized if the sequence number
	// for all transaction inputs is maxed out.
	for _, txIn := range msgTx.TxIn {
		if txIn.Sequence != math.MaxUint32 {
			return false
		}
	}
	return true
}

// CalcBlockSubsidy returns the subsidy amount a block at the provided height
// should have. This is mainly used for determining how much the coinbase for
// newly generated blocks awards as well as validating the coinbase for blocks
// has the expected value.
//
// The subsidy is halved every SubsidyReductionInterval blocks.  Mathematically
// this is: baseSubsidy / 2^(height/SubsidyReductionInterval)
//
// At the target block generation rate for the main network, this is
// approximately every 4 years.
func CalcBlockSubsidy(height int32, chainParams *chaincfg.Params) int64 {

	if chainParams.SubsidyReductionInterval == 0 {
		return baseSubsidy
	}

	halvings := uint(height / chainParams.SubsidyReductionInterval)

	if halvings == 0 {
		return baseSubsidy
	}

	// Equivalent to: baseSubsidy / (height/chainParams.SubsidyReductionInterval)
	subsidy := int64(baseSubsidy / halvings)
	// The minimum subsidy lasts for 1
	if subsidy == 0 {
		subsidy = 1
	}

	return subsidy
}

// CheckTransactionSanity performs some preliminary checks on a transaction to
// ensure it is sane.  These checks are context free.
func CheckTransactionSanity(tx *czzutil.Tx, magneticAnomalyActive bool, scriptFlags txscript.ScriptFlags) error {
	// A transaction must have at least one input.
	msgTx := tx.MsgTx()
	if len(msgTx.TxIn) == 0 {
		return ruleError(ErrNoTxInputs, "transaction has no inputs")
	}

	// A transaction must have at least one output.
	if len(msgTx.TxOut) == 0 {
		return ruleError(ErrNoTxOutputs, "transaction has no outputs")
	}

	// A transaction must not exceed the maximum allowed block payload when
	// serialized.
	serializedTxSize := tx.MsgTx().SerializeSize()
	if serializedTxSize > MaxTransactionSize {
		str := fmt.Sprintf("serialized transaction is too big - got "+
			"%d, max %d", serializedTxSize, MaxTransactionSize)
		return ruleError(ErrTxTooBig, str)
	}
	if magneticAnomalyActive && serializedTxSize < MinTransactionSize {
		str := fmt.Sprintf("serialized transaction is too small - got "+
			"%d, min %d", serializedTxSize, MinTransactionSize)
		return ruleError(ErrTxTooSmall, str)
	}

	sigOps := CountSigOps(tx, scriptFlags)
	if sigOps > MaxTransactionSigOps {
		str := fmt.Sprintf("transaction has too many sigops - got "+
			"%d, max %d", sigOps, MaxTransactionSigOps)
		return ruleError(ErrTxTooManySigOps, str)
	}

	// Ensure the transaction amounts are in range.  Each transaction
	// output must not be negative or more than the max allowed per
	// transaction.  Also, the total of all outputs must abide by the same
	// restrictions.  All amounts in a transaction are in a unit value known
	// as a satoshi.  One bitcoin is a quantity of satoshi as defined by the
	// SatoshiPerBitcoin constant.
	var totalSatoshi int64
	for _, txOut := range msgTx.TxOut {
		satoshi := txOut.Value
		if satoshi < 0 {
			str := fmt.Sprintf("transaction output has negative "+
				"value of %v", satoshi)
			return ruleError(ErrBadTxOutValue, str)
		}
		if satoshi > czzutil.MaxSatoshi {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v", satoshi,
				czzutil.MaxSatoshi)
			return ruleError(ErrBadTxOutValue, str)
		}

		// Two's complement int64 overflow guarantees that any overflow
		// is detected and reported.  This is impossible for Bitcoin, but
		// perhaps possible if an alt increases the total money supply.
		totalSatoshi += satoshi
		if totalSatoshi < 0 {
			str := fmt.Sprintf("total value of all transaction "+
				"outputs exceeds max allowed value of %v",
				czzutil.MaxSatoshi)
			return ruleError(ErrBadTxOutValue, str)
		}
		if totalSatoshi > czzutil.MaxSatoshi {
			str := fmt.Sprintf("total value of all transaction "+
				"outputs is %v which is higher than max "+
				"allowed value of %v", totalSatoshi,
				czzutil.MaxSatoshi)
			return ruleError(ErrBadTxOutValue, str)
		}
	}

	// Check for duplicate transaction inputs.
	existingTxOut := make(map[wire.OutPoint]struct{})
	for _, txIn := range msgTx.TxIn {
		if _, exists := existingTxOut[txIn.PreviousOutPoint]; exists {
			return ruleError(ErrDuplicateTxInputs, "transaction "+
				"contains duplicate inputs")
		}
		existingTxOut[txIn.PreviousOutPoint] = struct{}{}
	}

	// Coinbase script length must be between min and max length.
	if IsCoinBase(tx) {
		slen := len(msgTx.TxIn[0].SignatureScript)
		if slen < MinCoinbaseScriptLen || slen > MaxCoinbaseScriptLen {
			str := fmt.Sprintf("coinbase transaction script length "+
				"of %d is out of range (min: %d, max: %d)",
				slen, MinCoinbaseScriptLen, MaxCoinbaseScriptLen)
			return ruleError(ErrBadCoinbaseScriptLen, str)
		}
	} else {
		// Previous transaction outputs referenced by the inputs to this
		// transaction must not be null.
		for _, txIn := range msgTx.TxIn {
			if isNullOutpoint(&txIn.PreviousOutPoint) {
				return ruleError(ErrBadTxInput, "transaction "+
					"input refers to previous output that "+
					"is null")
			}
		}
	}

	return nil
}

func (b *BlockChain) checkEntangleTx(tx *czzutil.Tx) error {
	// tmp the cache is nil
	_, err := b.GetEntangleVerify().VerifyEntangleTx(tx.MsgTx())
	if err != nil {
		return err
	}
	return nil
}
func (b *BlockChain) CheckBlockEntangle(block *czzutil.Block) error {
	curHeight := int64(0)
	for _, tx := range block.Transactions() {
		einfos, _ := cross.IsEntangleTx(tx.MsgTx())
		if einfos == nil {
			continue
		}
		max := int64(0)
		for _, ii := range einfos {
			if max < int64(ii.Height) {
				max = int64(ii.Height)
			}
		}
		if curHeight > max {
			return errors.New("unordered entangle tx in the block")
		}
		err := b.checkEntangleTx(tx)
		if err != nil {
			return err
		}
	}
	return nil
}
func checkTxSequence(block *czzutil.Block, utxoView *UtxoViewpoint, chainParams *chaincfg.Params) error {
	height := block.Height()
	if chainParams.EntangleHeight >= height {
		return nil
	}
	infos, err := getEtsInfoInBlock(block, utxoView, chainParams)
	if err != nil {
		return err
	}
	return cross.VerifyTxsSequence(infos)
}

// checkProofOfWork ensures the block header bits which indicate the target
// difficulty is in min/max range and that the block hash is less than the
// target difficulty as claimed.
//
// The flags modify the behavior of this function as follows:
//  - BFNoPoWCheck: The check to ensure the block hash is less than the target
//    difficulty is not performed.
func checkProofOfWork(header *wire.BlockHeader, powLimit *big.Int, flags BehaviorFlags) error {
	// The target difficulty must be larger than zero.
	target := CompactToBig(header.Bits)
	if target.Sign() <= 0 {
		str := fmt.Sprintf("block target difficulty of %064x is too low",
			target)
		return ruleError(ErrUnexpectedDifficulty, str)
	}

	// The target difficulty must be less than the maximum allowed.
	if target.Cmp(powLimit) > 0 {
		str := fmt.Sprintf("block target difficulty of %064x is "+
			"higher than max of %064x", target, powLimit)
		return ruleError(ErrUnexpectedDifficulty, str)
	}

	// The block hash must be less than the claimed target unless the flag
	// to avoid proof of work checks is set.
	if !flags.HasFlag(BFNoPoWCheck) {
		// The block hash must be less than the claimed target.
		hash := header.BlockHashNoNonce()
		param := &consensus.CzzConsensusParam{
			HeadHash: hash,
			Target:   target,
		}
		//fmt.Println("val", hash.String())
		//fmt.Println("target", target)

		if err := consensus.VerifyBlockSeal(param, header.Nonce); err != nil {
			str := fmt.Sprintf("block hash of %064x is higher than "+
				"expected max of %064x", header.BlockHash(), target)
			return ruleError(ErrHighHash, str)
		}
		// hashNum := HashToBig(&hash)
		// if hashNum.Cmp(target) > 0 {
		// 	str := fmt.Sprintf("block hash of %064x is higher than "+
		// 		"expected max of %064x", hashNum, target)
		// 	return ruleError(ErrHighHash, str)
		// }
	}

	return nil
}

// CheckProofOfWork ensures the block header bits which indicate the target
// difficulty is in min/max range and that the block hash is less than the
// target difficulty as claimed.
func CheckProofOfWork(block *czzutil.Block, powLimit *big.Int) error {
	return checkProofOfWork(&block.MsgBlock().Header, powLimit, BFNone)
}

// CountSigOps returns the number of signature operations for all transaction
// input and output scripts in the provided transaction.  This uses the
// quicker, but imprecise, signature operation counting mechanism from
// txscript.
func CountSigOps(tx *czzutil.Tx, scriptFlags txscript.ScriptFlags) int {
	msgTx := tx.MsgTx()

	// Accumulate the number of signature operations in all transaction
	// inputs.
	totalSigOps := 0
	for _, txIn := range msgTx.TxIn {
		numSigOps := txscript.GetSigOpCount(txIn.SignatureScript, scriptFlags)
		totalSigOps += numSigOps
	}

	// Accumulate the number of signature operations in all transaction
	// outputs.
	for _, txOut := range msgTx.TxOut {
		numSigOps := txscript.GetSigOpCount(txOut.PkScript, scriptFlags)
		totalSigOps += numSigOps
	}

	return totalSigOps
}

// GetSigOps returns the unified sig op count for the passed transaction
// respecting current active soft-forks which modified sig op cost counting.
func GetSigOps(tx *czzutil.Tx, isCoinBaseTx bool, utxoView *UtxoViewpoint, scriptFlags txscript.ScriptFlags) (int, error) {
	numSigOps := CountSigOps(tx, scriptFlags)
	if scriptFlags.HasFlag(txscript.ScriptBip16) {
		numP2SHSigOps, err := CountP2SHSigOps(tx, isCoinBaseTx, utxoView, scriptFlags)
		if err != nil {
			return 0, nil
		}
		numSigOps += numP2SHSigOps
	}
	return numSigOps, nil
}

// CountP2SHSigOps returns the number of signature operations for all input
// transactions which are of the pay-to-script-hash type.  This uses the
// precise, signature operation counting mechanism from the script engine which
// requires access to the input transaction scripts.
func CountP2SHSigOps(tx *czzutil.Tx, isCoinBaseTx bool, utxoView *UtxoViewpoint, scriptFlags txscript.ScriptFlags) (int, error) {
	// Coinbase transactions have no interesting inputs.
	if isCoinBaseTx {
		return 0, nil
	}

	// Accumulate the number of signature operations in all transaction
	// inputs.
	msgTx := tx.MsgTx()
	totalSigOps := 0
	for txInIndex, txIn := range msgTx.TxIn {
		// Ensure the referenced input transaction is available.
		utxo := utxoView.LookupEntry(txIn.PreviousOutPoint)
		if utxo == nil {
			str := fmt.Sprintf("output %v referenced from "+
				"transaction %s:%d does not exist", txIn.PreviousOutPoint,
				tx.Hash(), txInIndex)
			return 0, ruleError(ErrMissingTxOut, str)
		} else if utxo.IsSpent() {
			str := fmt.Sprintf("output %v referenced from "+
				"transaction %s:%d has already been spent", txIn.PreviousOutPoint,
				tx.Hash(), txInIndex)
			return 0, ruleError(ErrSpentTxOut, str)
		}

		// We're only interested in pay-to-script-hash types, so skip
		// this input if it's not one.
		pkScript := utxo.PkScript()
		if !txscript.IsPayToScriptHash(pkScript) {
			continue
		}

		// Count the precise number of signature operations in the
		// referenced public key script.
		sigScript := txIn.SignatureScript
		numSigOps := txscript.GetPreciseSigOpCount(sigScript, pkScript,
			scriptFlags)

		// We could potentially overflow the accumulator so check for
		// overflow.
		lastSigOps := totalSigOps
		totalSigOps += numSigOps
		if totalSigOps < lastSigOps {
			str := fmt.Sprintf("the public key script from output "+
				"%v contains too many signature operations - "+
				"overflow", txIn.PreviousOutPoint)
			return 0, ruleError(ErrTooManySigOps, str)
		}
	}

	return totalSigOps, nil
}

// checkBlockHeaderSanity performs some preliminary checks on a block header to
// ensure it is sane before continuing with processing.  These checks are
// context free.
//
// The flags do not modify the behavior of this function directly, however they
// are needed to pass along to checkProofOfWork.
func checkBlockHeaderSanity(bc *BlockChain, header *wire.BlockHeader, powLimit *big.Int, timeSource MedianTimeSource, flags BehaviorFlags) error {
	// Ensure the proof of work bits in the block header is in min/max range
	// and the block hash is less than the target value described by the
	// bits.
	err := checkProofOfWork(header, powLimit, flags)
	if err != nil {
		return err
	}

	// A block timestamp must not have a greater precision than one second.
	// This check is necessary because Go time.Time values support
	// nanosecond precision whereas the consensus rules only apply to
	// seconds and it's much nicer to deal with standard Go time values
	// instead of converting to seconds everywhere.
	if !header.Timestamp.Equal(time.Unix(header.Timestamp.Unix(), 0)) {
		str := fmt.Sprintf("block timestamp of %v has a higher "+
			"precision than one second", header.Timestamp)
		return ruleError(ErrInvalidTime, str)
	}

	if header.Timestamp.After(time.Now().Add(allowedFutureBlockTime)) {
		str := fmt.Sprintf("block timestamp of %v > time.Now()", header.Timestamp)
		return ruleError(ErrInvalidTime, str)
	}

	prevblock, err := bc.BlockByHash(&header.PrevBlock)
	if err != nil && int64(bc.chainParams.Deployments[chaincfg.DeploymentSEQ].StartTime) < header.Timestamp.Unix() && prevblock != nil && prevblock.MsgBlock().Header.Timestamp.After(header.Timestamp) {
		str := fmt.Sprintf("prevheader timestamp %v > header %v", prevblock.MsgBlock().Header.Timestamp, header.Timestamp)
		return ruleError(ErrInvalidTime, str)
	}

	// Ensure the block time is not too far in the future.
	maxTimestamp := timeSource.AdjustedTime().Add(time.Second * MaxTimeOffsetSeconds)
	if header.Timestamp.After(maxTimestamp) {
		str := fmt.Sprintf("block timestamp of %v is too far in the "+
			"future", header.Timestamp)
		return ruleError(ErrTimeTooNew, str)
	}

	return nil
}

// checkBlockSanity performs some preliminary checks on a block to ensure it is
// sane before continuing with block processing.  These checks are context free.
//
// The flags do not modify the behavior of this function directly, however they
// are needed to pass along to checkBlockHeaderSanity.
func checkBlockSanity(b *BlockChain, block *czzutil.Block, powLimit *big.Int, timeSource MedianTimeSource, flags BehaviorFlags) error {
	msgBlock := block.MsgBlock()
	header := &msgBlock.Header

	err := checkBlockHeaderSanity(b, header, powLimit, timeSource, flags)
	if err != nil {
		return err
	}

	// A block must have at least one transaction.
	numTx := len(msgBlock.Transactions)
	if numTx == 0 {
		return ruleError(ErrNoTransactions, "block does not contain "+
			"any transactions")
	}

	// The first transaction in a block must be a coinbase.
	transactions := block.Transactions()
	if !IsCoinBase(transactions[0]) {
		return ruleError(ErrFirstTxNotCoinbase, "first transaction in "+
			"block is not a coinbase")
	}

	// A block must not have more than one coinbase.
	for i, tx := range transactions[1:] {
		if IsCoinBase(tx) {
			str := fmt.Sprintf("block contains second coinbase at "+
				"index %d", i+1)
			return ruleError(ErrMultipleCoinbases, str)
		}
	}

	magneticAnomaly := flags.HasFlag(BFMagneticAnomaly)

	// TODO: This is not a full set of ScriptFlags and only
	// covers the Nov 2018 fork.
	var scriptFlags txscript.ScriptFlags
	if magneticAnomaly {
		scriptFlags |= txscript.ScriptVerifySigPushOnly |
			txscript.ScriptVerifyCleanStack |
			txscript.ScriptVerifyCheckDataSig
	}

	// Do some preliminary checks on each transaction to ensure they are
	// sane before continuing.
	var lastTxid *chainhash.Hash
	for i, tx := range transactions {
		// If MagneticAnomaly is active validate the CTOR consensus rule, skipping
		// the coinbase transaction.
		if magneticAnomaly && i > 1 && lastTxid.Compare(tx.Hash()) >= 0 {
			return ruleError(ErrInvalidTxOrder, "transactions are not in lexicographical order")
		}
		lastTxid = tx.Hash()
		err := CheckTransactionSanity(tx, magneticAnomaly, scriptFlags)
		if err != nil {
			return err
		}
	}

	// Build merkle tree and ensure the calculated merkle root matches the
	// entry in the block header.  This also has the effect of caching all
	// of the transaction hashes in the block to speed up future hash
	// checks.  Bitcoind builds the tree here and checks the merkle root
	// after the following checks, but there is no reason not to check the
	// merkle root matches here.
	merkles := BuildMerkleTreeStore(block.Transactions())
	calculatedMerkleRoot := merkles[len(merkles)-1]
	if !header.MerkleRoot.IsEqual(calculatedMerkleRoot) {
		str := fmt.Sprintf("block merkle root is invalid - block "+
			"header indicates %v, but calculated value is %v",
			header.MerkleRoot, calculatedMerkleRoot)
		return ruleError(ErrBadMerkleRoot, str)
	}

	// Check for duplicate transactions.  This check will be fairly quick
	// since the transaction hashes are already cached due to building the
	// merkle tree above.
	existingTxHashes := make(map[chainhash.Hash]struct{})
	for _, tx := range transactions {
		hash := tx.Hash()
		if _, exists := existingTxHashes[*hash]; exists {
			str := fmt.Sprintf("block contains duplicate "+
				"transaction %v", hash)
			return ruleError(ErrDuplicateTx, str)
		}
		existingTxHashes[*hash] = struct{}{}
	}

	return nil
}

// CheckBlockSanity performs some preliminary checks on a block to ensure it is
// sane before continuing with block processing.  These checks are context free.
func CheckBlockSanity(b *BlockChain, block *czzutil.Block, powLimit *big.Int, timeSource MedianTimeSource, magneticAnomalyActive bool) error {
	behaviorFlags := BFNone

	if magneticAnomalyActive {
		behaviorFlags |= BFMagneticAnomaly
	}
	return checkBlockSanity(b, block, powLimit, timeSource, behaviorFlags)
}

// ExtractCoinbaseHeight attempts to extract the height of the block from the
// scriptSig of a coinbase transaction.
func ExtractCoinbaseHeight(coinbaseTx *czzutil.Tx) (int32, error) {
	sigScript := coinbaseTx.MsgTx().TxIn[0].SignatureScript
	if len(sigScript) < 1 {
		str := "the coinbase signature script for greater must start " +
			"with the length of the serialized block height"
		return 0, ruleError(ErrMissingCoinbaseHeight, str)
	}

	// Detect the case when the block height is a small integer encoded with
	// as single byte.
	opcode := int(sigScript[0])
	if opcode == txscript.OP_0 {
		return 0, nil
	}
	if opcode >= txscript.OP_1 && opcode <= txscript.OP_16 {
		return int32(opcode - (txscript.OP_1 - 1)), nil
	}

	// Otherwise, the opcode is the length of the following bytes which
	// encode in the block height.
	serializedLen := int(sigScript[0])
	if len(sigScript[1:]) < serializedLen {
		str := "the coinbase signature script for blocks of " +
			"version %d or greater must start with the " +
			"serialized block height"
		str = fmt.Sprintf(str, serializedLen)
		return 0, ruleError(ErrMissingCoinbaseHeight, str)
	}

	serializedHeightBytes := make([]byte, 8)
	copy(serializedHeightBytes, sigScript[1:serializedLen+1])
	serializedHeight := binary.LittleEndian.Uint64(serializedHeightBytes)

	return int32(serializedHeight), nil
}

// checkSerializedHeight checks if the signature script in the passed
// transaction starts with the serialized block height of wantHeight.
func checkSerializedHeight(coinbaseTx *czzutil.Tx, wantHeight int32) error {
	serializedHeight, err := ExtractCoinbaseHeight(coinbaseTx)
	if err != nil {
		return err
	}

	if serializedHeight != wantHeight {
		str := fmt.Sprintf("the coinbase signature script serialized "+
			"block height is %d when %d was expected",
			serializedHeight, wantHeight)
		return ruleError(ErrBadCoinbaseHeight, str)
	}
	return nil
}

// CheckBlockHeaderContext checks the passed block header against the best chain.
// In other words it checks if the header would be considered valid if processed
// along with the next block.
//
// This function is safe for concurrent access.
func (b *BlockChain) CheckBlockHeaderContext(header *wire.BlockHeader) error {
	b.chainLock.Lock()
	defer b.chainLock.Unlock()

	flags := BFNone

	tip := b.bestChain.Tip()

	err := checkBlockHeaderSanity(b, header, b.chainParams.PowLimit, b.timeSource, flags)
	if err != nil {
		return err
	}

	return b.checkBlockHeaderContext(header, tip, flags)
}

// checkBlockHeaderContext performs several validation checks on the block header
// which depend on its position within the block chain.
//
// The flags modify the behavior of this function as follows:
//  - BFFastAdd: All checks except those involving comparing the header against
//    the checkpoints are not performed.
//
// This function MUST be called with the chain state lock held (for writes).
func (b *BlockChain) checkBlockHeaderContext(header *wire.BlockHeader, prevNode *blockNode, flags BehaviorFlags) error {
	// The height of this block is one more than the referenced previous
	// block.
	blockHeight := prevNode.height + 1

	fastAdd := flags.HasFlag(BFFastAdd)
	if !fastAdd {
		// Ensure the difficulty specified in the block header matches
		// the calculated difficulty based on the previous block and
		// difficulty retarget rules.
		expectedDifficulty, err := b.calcNextRequiredDifficulty(prevNode, header.Timestamp)

		log.Debug("checkBlockHeaderContext", "blockHeight", blockHeight, "hash", header.BlockHash(), "header", header.Timestamp)
		if err != nil {
			return err
		}
		blockDifficulty := header.Bits
		if blockDifficulty != expectedDifficulty {
			str := "block difficulty of %d is not the expected value of %d"
			str = fmt.Sprintf(str, blockDifficulty, expectedDifficulty)
			return ruleError(ErrUnexpectedDifficulty, str)
		}

		// Ensure the timestamp for the block header is after the
		// median time of the last several blocks (medianTimeBlocks).
		medianTime := prevNode.CalcPastMedianTime()
		if !header.Timestamp.After(medianTime) {
			str := "block timestamp of %v is not after expected %v"
			str = fmt.Sprintf(str, header.Timestamp, medianTime)
			return ruleError(ErrTimeTooOld, str)
		}
	}

	// Ensure chain matches up to predetermined checkpoints.
	blockHash := header.BlockHash()
	if !b.verifyCheckpoint(blockHeight, &blockHash) {
		str := fmt.Sprintf("block at height %d does not match "+
			"checkpoint hash", blockHeight)
		return ruleError(ErrBadCheckpoint, str)
	}

	// Find the previous checkpoint and prevent blocks which fork the main
	// chain before it.  This prevents storage of new, otherwise valid,
	// blocks which build off of old blocks that are likely at a much easier
	// difficulty and therefore could be used to waste cache and disk space.
	checkpointNode, err := b.findPreviousCheckpoint()
	if err != nil {
		return err
	}
	if checkpointNode != nil && blockHeight < checkpointNode.height {
		str := fmt.Sprintf("block at height %d forks the main chain "+
			"before the previous checkpoint at height %d",
			blockHeight, checkpointNode.height)
		return ruleError(ErrForkTooOld, str)
	}

	return nil
}

// checkBlockContext peforms several validation checks on the block which depend
// on its position within the block chain.
//
// The flags modify the behavior of this function as follows:
//  - BFFastAdd: The transaction are not checked to see if they are finalized
//    and the somewhat expensive BIP0034 validation is not performed.
//
// The flags are also passed to checkBlockHeaderContext.  See its documentation
// for how the flags modify its behavior.
//
// This function MUST be called with the chain state lock held (for writes).
func (b *BlockChain) checkBlockContext(block *czzutil.Block, prevNode *blockNode, flags BehaviorFlags) error {
	// Perform all block header related validation checks.
	header := &block.MsgBlock().Header
	err := b.checkBlockHeaderContext(header, prevNode, flags)
	if err != nil {
		return err
	}

	// A block must not have more transactions than the max block payload or
	// else it is certainly over the size limit.
	// We need to check the blocksize here rather than in checkBlockSanity
	// because after the Uahf activation it is not longer context free as
	// the max size depends on whether Uahf has activated or not.
	maxBlockSize := b.MaxBlockSize()
	numTx := len(block.MsgBlock().Transactions)
	if numTx > maxBlockSize {
		str := fmt.Sprintf("block contains too many transactions - "+
			"got %d, max %d", numTx, maxBlockSize)
		return ruleError(ErrBlockTooBig, str)
	}

	// The August 1st, 2017 (Uahf) hardfork has a consensus rule which
	// says the first block after the fork date must be larger than 1MB.
	// We can skip this check on regtest and simnet.
	// if (b.chainParams == &chaincfg.MainNetParams || b.chainParams == &chaincfg.TestNet3Params) &&
	// 	block.Height() == b.chainParams.UahfForkHeight+1 {
	// 	if block.MsgBlock().SerializeSize() <= LegacyMaxBlockSize {
	// 		str := fmt.Sprintf("the first block after uahf fork block is not greater than 1MB")
	// 		return ruleError(ErrBlockTooSmall, str)
	// 	}
	// }

	// A block must not exceed the maximum allowed block payload when
	// serialized.
	serializedSize := block.MsgBlock().SerializeSize()
	if serializedSize > maxBlockSize {
		str := fmt.Sprintf("serialized block is too big - got %d, "+
			"max %d", serializedSize, maxBlockSize)
		return ruleError(ErrBlockTooBig, str)
	}

	fastAdd := flags.HasFlag(BFFastAdd)
	if !fastAdd {
		// Obtain the latest state of the deployed CSV soft-fork in
		// order to properly guard the new validation behavior based on
		// the current BIP 9 version bits state.
		csvState, err := b.deploymentState(prevNode, chaincfg.DeploymentCSV)
		if err != nil {
			return err
		}

		// Once the CSV soft-fork is fully active, we'll switch to
		// using the current median time past of the past block's
		// timestamps for all lock-time based checks.
		blockTime := header.Timestamp
		if csvState == ThresholdActive {
			blockTime = prevNode.CalcPastMedianTime()
		}

		// The height of this block is one more than the referenced
		// previous block.
		blockHeight := prevNode.height + 1

		// Ensure all transactions in the block are finalized.
		for _, tx := range block.Transactions() {
			if !IsFinalizedTransaction(tx, blockHeight,
				blockTime) {

				str := fmt.Sprintf("block contains unfinalized "+
					"transaction %v", tx.Hash())
				return ruleError(ErrUnfinalizedTx, str)
			}
		}

		coinbaseTx := block.Transactions()[0]
		err = checkSerializedHeight(coinbaseTx, blockHeight)
		if err != nil {
			return err
		}
	}

	return nil
}
func checkMergeTxInCoinbase(tx *czzutil.Tx, txHeight int32, utxoView *UtxoViewpoint, chainParams *chaincfg.Params) (bool, error) {
	if chainParams.EntangleHeight >= txHeight {
		if isCoinBaseInParam(tx, chainParams) {
			return true, nil
		}
	} else {
		if isCoinBaseInParam(tx, chainParams) {
			for txInIndex, txIn := range tx.MsgTx().TxIn {
				if txInIndex == 0 {
					continue
				}
				// Ensure the referenced input transaction is available.
				utxo := utxoView.LookupEntry(txIn.PreviousOutPoint)
				if utxo == nil {
					str := fmt.Sprintf("output %v referenced from "+
						"transaction %s:%d does not exist", txIn.PreviousOutPoint,
						tx.Hash(), txInIndex)
					return true, ruleError(ErrMissingTxOut, str)
				} else if utxo.IsSpent() {
					str := fmt.Sprintf("output %v referenced from "+
						"transaction %s:%d has already been spent", txIn.PreviousOutPoint,
						tx.Hash(), txInIndex)
					return true, ruleError(ErrSpentTxOut, str)
				}
				uxtoHeight := utxo.BlockHeight()
				if uxtoHeight < chainParams.EntangleHeight-1 {
					str := fmt.Sprintf("output %v referenced from "+
						"the wrong height[%d,%d]", txIn.PreviousOutPoint,
						uxtoHeight, chainParams.EntangleHeight-1)
					return true, ruleError(ErrBadTxOutValue, str)
				}
				if txInIndex <= 2 {
					if err := matchPoolFromUtxo(utxo, txInIndex, chainParams); err != nil {
						return true, nil
					}
				}
			}
			return true, nil
		}
	}
	return false, nil
}
func checkBlockSubsidy(block, preBlock *czzutil.Block, txHeight int32, utxoView *UtxoViewpoint, amountSubsidy int64, chainParams *chaincfg.Params) error {
	if txHeight <= chainParams.EntangleHeight {
		return nil
	}
	originIncome1, originIncome2 := amountSubsidy*19/100, amountSubsidy/100
	originIncome3 := amountSubsidy - originIncome1 - originIncome2
	if txHeight == chainParams.EntangleHeight {
		originIncome1 = originIncome1 * int64(chainParams.EntangleHeight-1)
		originIncome2 = originIncome2 * int64(chainParams.EntangleHeight-1)
	}
	reward1, reward2, reward3 := originIncome1, originIncome2, originIncome3
	// check sum reward
	summay, err := summayOfTxsAndCheck(preBlock, block, utxoView, reward3, reward1, reward2)
	if err != nil {
		return err
	}
	// check pool1 reward
	expPool1Amount := summay.lastpool1Amount + originIncome1 - summay.EntangleAmount
	if summay.pool1Amount != expPool1Amount {
		return errors.New(fmt.Sprintf("BlockSubsidy:the pool1 address's reward was wrong[%v,expected:%v] height:%d ",
			summay.pool1Amount, expPool1Amount, txHeight))
	}
	// check pool2 reward
	if originIncome2+summay.lastpool2Amount != summay.pool2Amount {
		return errors.New(fmt.Sprintf("BlockSubsidy:the pool2 address's reward was wrong[%v,expected:%v] height:%d ",
			summay.pool2Amount, originIncome2+summay.lastpool2Amount, txHeight))
	}
	if summay.TotalOut > summay.TotalIn {
		return errors.New(fmt.Sprintf("BlockSubsidy:wrong,the totalOut > totalIn,[totalOut:%v,totalIn:%v] height:%d",
			summay.TotalOut, summay.TotalIn, txHeight))
	}
	return nil
}

// CheckTransactionInputs performs a series of checks on the inputs to a
// transaction to ensure they are valid.  An example of some of the checks
// include verifying all inputs exist, ensuring the coinbase seasoning
// requirements are met, detecting double spends, validating all values and fees
// are in the legal range and the total output amount doesn't exceed the input
// amount, and verifying the signatures to prove the spender was the owner of
// the bitcoins and therefore allowed to spend them.  As it checks the inputs,
// it also calculates the total fees for the transaction and returns that value.
//
// NOTE: The transaction MUST have already been sanity checked with the
// CheckTransactionSanity function prior to calling this function.
func CheckTransactionInputs(tx *czzutil.Tx, txHeight int32, utxoView *UtxoViewpoint, chainParams *chaincfg.Params) (int64, error) {
	// Coinbase transactions have no inputs.
	// if IsCoinBase(tx) {
	// 	return 0, nil
	// }

	if ok, err := checkMergeTxInCoinbase(tx, txHeight, utxoView, chainParams); ok {
		return 0, err
	}

	txHash := tx.Hash()
	var totalSatoshiIn int64
	for txInIndex, txIn := range tx.MsgTx().TxIn {
		// Ensure the referenced input transaction is available.
		utxo := utxoView.LookupEntry(txIn.PreviousOutPoint)
		if utxo == nil {
			str := fmt.Sprintf("output %v referenced from "+
				"transaction %s:%d does not exist", txIn.PreviousOutPoint,
				tx.Hash(), txInIndex)
			return 0, ruleError(ErrMissingTxOut, str)
		} else if utxo.IsSpent() {
			str := fmt.Sprintf("output %v referenced from "+
				"transaction %s:%d has already been spent", txIn.PreviousOutPoint,
				tx.Hash(), txInIndex)
			return 0, ruleError(ErrSpentTxOut, str)
		}

		// Ensure the transaction is not spending coins which have not
		// yet reached the required coinbase maturity.
		if utxo.IsCoinBase() && !utxo.IsPool() {
			originHeight := utxo.BlockHeight()
			blocksSincePrev := txHeight - originHeight
			coinbaseMaturity := int32(chainParams.CoinbaseMaturity)
			if blocksSincePrev < coinbaseMaturity {
				str := fmt.Sprintf("tried to spend coinbase "+
					"transaction output %v from height %v "+
					"at height %v before required maturity "+
					"of %v blocks", txIn.PreviousOutPoint,
					originHeight, txHeight,
					coinbaseMaturity)
				return 0, ruleError(ErrImmatureSpend, str)
			}
		}

		// Ensure the transaction amounts are in range.  Each of the
		// output values of the input transactions must not be negative
		// or more than the max allowed per transaction.  All amounts in
		// a transaction are in a unit value known as a satoshi.  One
		// bitcoin is a quantity of satoshi as defined by the
		// SatoshiPerBitcoin constant.
		originTxSatoshi := utxo.Amount()
		if originTxSatoshi < 0 {
			str := fmt.Sprintf("transaction output has negative "+
				"value of %v", czzutil.Amount(originTxSatoshi))
			return 0, ruleError(ErrBadTxOutValue, str)
		}
		if originTxSatoshi > czzutil.MaxSatoshi {
			str := fmt.Sprintf("transaction output value of %v is "+
				"higher than max allowed value of %v",
				czzutil.Amount(originTxSatoshi),
				czzutil.MaxSatoshi)
			return 0, ruleError(ErrBadTxOutValue, str)
		}

		// The total of all outputs must not be more than the max
		// allowed per transaction.  Also, we could potentially overflow
		// the accumulator so check for overflow.
		lastSatoshiIn := totalSatoshiIn
		totalSatoshiIn += originTxSatoshi
		if totalSatoshiIn < lastSatoshiIn ||
			totalSatoshiIn > czzutil.MaxSatoshi {
			str := fmt.Sprintf("total value of all transaction "+
				"inputs is %v which is higher than max "+
				"allowed value of %v", totalSatoshiIn,
				czzutil.MaxSatoshi)
			return 0, ruleError(ErrBadTxOutValue, str)
		}
	}

	// Calculate the total output amount for this transaction.  It is safe
	// to ignore overflow and out of range errors here because those error
	// conditions would have already been caught by checkTransactionSanity.
	var totalSatoshiOut int64
	for _, txOut := range tx.MsgTx().TxOut {
		totalSatoshiOut += txOut.Value
	}

	// Ensure the transaction does not spend more than its inputs.
	if totalSatoshiIn < totalSatoshiOut {
		str := fmt.Sprintf("total value of all transaction inputs for "+
			"transaction %v is %v which is less than the amount "+
			"spent of %v", txHash, totalSatoshiIn, totalSatoshiOut)
		return 0, ruleError(ErrSpendTooHigh, str)
	}

	// NOTE: bitcoind checks if the transaction fees are < 0 here, but that
	// is an impossible condition because of the check above that ensures
	// the inputs are >= the outputs.
	txFeeInSatoshi := totalSatoshiIn - totalSatoshiOut
	return txFeeInSatoshi, nil
}

// checkConnectBlock performs several checks to confirm connecting the passed
// block to the chain represented by the passed view does not violate any rules.
// In addition, the passed view is updated to spend all of the referenced
// outputs and add all of the new utxos created by block.  Thus, the view will
// represent the state of the chain as if the block were actually connected and
// consequently the best hash for the view is also updated to passed block.
//
// An example of some of the checks performed are ensuring connecting the block
// would not cause any duplicate transaction hashes for old transactions that
// aren't already fully spent, double spends, exceeding the maximum allowed
// signature operations per block, invalid values in relation to the expected
// block subsidy, or fail transaction script validation.
//
// The CheckConnectBlockTemplate function makes use of this function to perform
// the bulk of its work.  The only difference is this function accepts a node
// which may or may not require reorganization to connect it to the main chain
// whereas CheckConnectBlockTemplate creates a new node which specifically
// connects to the end of the current main chain and then calls this function
// with that node.
//
// This function MUST be called with the chain state lock held (for writes).
func (b *BlockChain) checkConnectBlock(node *blockNode, block *czzutil.Block, view *UtxoViewpoint, stxos *[]SpentTxOut) error {
	// If the side chain blocks end up in the database, a call to
	// CheckBlockSanity should be done here in case a previous version
	// allowed a block that is no longer valid.  However, since the
	// implementation only currently uses memory for the side chain blocks,
	// it isn't currently necessary.

	// The coinbase for the Genesis block is not spendable, so just return
	// an error now.
	if node.hash.IsEqual(b.chainParams.GenesisHash) {
		str := "the coinbase for the genesis block is not spendable"
		return ruleError(ErrMissingTxOut, str)
	}
	// Load all of the utxos referenced by the inputs for all transactions
	// in the block don't already exist in the utxo view from the cache.
	//
	// These utxo entries are needed for verification of things such as
	// transaction inputs, counting pay-to-script-hashes, and scripts.
	err := view.addInputUtxos(b.utxoCache, block)
	if err != nil {
		return err
	}

	// Blocks need to have the pay-to-script-hash checks enabled.
	var scriptFlags txscript.ScriptFlags

	scriptFlags |= txscript.ScriptBip16
	// Enforce DER signatures
	scriptFlags |= txscript.ScriptVerifyDERSignatures

	// Enforce CHECKLOCKTIMEVERIFY
	scriptFlags |= txscript.ScriptVerifyCheckLockTimeVerify

	// we must enforce strict encoding on all signatures and enforce
	// the replay protected sighash.
	scriptFlags |= txscript.ScriptVerifyStrictEncoding | txscript.ScriptVerifyBip143SigHash

	// If Daa is active enforce Low S and Nullfail script validation rules.
	scriptFlags |= txscript.ScriptVerifyLowS | txscript.ScriptVerifyNullFail

	// If MagneticAnomaly hardfork is active we must enforce PushOnly and CleanStack
	// and enable OP_CHECKDATASIG and OP_CHECKDATASIGVERIFY.
	scriptFlags |= txscript.ScriptVerifySigPushOnly |
		txscript.ScriptVerifyCleanStack |
		txscript.ScriptVerifyCheckDataSig

	// If GreatWall is enforce Schnorr and AllowSegwitRecovery script flags.
	scriptFlags |= txscript.ScriptVerifySchnorr | txscript.ScriptVerifyAllowSegwitRecovery

	// The number of signature operations must be less than the maximum
	// allowed per block.  Note that the preliminary sanity checks on a
	// block also include a check similar to this one, but this check
	// expands the count to include a precise count of pay-to-script-hash
	// signature operations in each of the input transaction public key
	// scripts.
	transactions := block.Transactions()
	totalSigOpCost := 0
	nBlockBytes := block.MsgBlock().SerializeSize()
	maxSigOps := MaxBlockSigOps(uint32(nBlockBytes))
	for i, tx := range transactions {
		// Since the first (and only the first) transaction has
		// already been verified to be a coinbase transaction,
		// use i == 0 as an optimization for the flag to
		// countP2SHSigOps for whether or not the transaction is
		// a coinbase transaction rather than having to do a
		// full coinbase check again.
		sigOpCost, err := GetSigOps(tx, i == 0, view, scriptFlags)
		if err != nil {
			return err
		}

		// Check for overflow or going over the limits.  We have to do
		// this on every loop iteration to avoid overflow.
		lastSigOpCost := totalSigOpCost
		totalSigOpCost += sigOpCost
		if totalSigOpCost < lastSigOpCost || totalSigOpCost > maxSigOps {
			str := fmt.Sprintf("block contains too many "+
				"signature operations - got %v, max %v",
				totalSigOpCost, maxSigOps)
			return ruleError(ErrTooManySigOps, str)
		}
	}

	// Perform several checks on the inputs for each transaction.  Also
	// accumulate the total fees.  This could technically be combined with
	// the loop above instead of running another loop over the transactions,
	// but by separating it we can avoid running the more expensive (though
	// still relatively cheap as compared to running the scripts) checks
	// against all the inputs when the signature operations are out of
	// bounds.
	var totalFees int64
	for _, tx := range transactions {
		txFee, err := CheckTransactionInputs(tx, node.height, view, b.chainParams)
		if err != nil {
			return err
		}

		// Sum the total fees and ensure we don't overflow the
		// accumulator.
		lastTotalFees := totalFees
		totalFees += txFee
		if totalFees < lastTotalFees {
			return ruleError(ErrBadFees, "total fees for block "+
				"overflows accumulator")
		}

		// Add all of the outputs for this transaction which are not
		// provably unspendable as available utxos.  Also, the passed
		// spent txos slice is updated to contain an entry for each
		// spent txout in the order each transaction spends them.
		//
		// If magneticAnomaly is not active we connect each transaction
		// one at a time so that we can validate the topological order
		// in the process.
		// if !magneticAnomalyActive {
		// 	err = connectTransaction(view, tx, node.height, stxos, false)
		// 	if err != nil {
		// 		return err
		// 	}
		// }
	}
	if err := checkTxSequence(block, view, b.chainParams); err != nil {
		return err
	}
	// we can use Outputs-then-inputs validation to validate the utxos.
	err = connectTransactions(view, block, stxos, false)
	if err != nil {
		return nil
	}

	// The total output values of the coinbase transaction must not exceed
	// the expected subsidy value plus total transaction fees gained from
	// mining the block.  It is safe to ignore overflow and out of range
	// errors here because those error conditions would have already been
	// caught by checkTransactionSanity.
	var totalSatoshiOut int64
	for _, txOut := range transactions[0].MsgTx().TxOut {
		totalSatoshiOut += txOut.Value
		break
	}
	amountSubsidy := CalcBlockSubsidy(node.height, b.chainParams)
	expectedSatoshiOut := amountSubsidy + totalFees
	if totalSatoshiOut > expectedSatoshiOut {
		str := fmt.Sprintf("coinbase transaction for block pays %v "+
			"which is more than expected value of %v",
			totalSatoshiOut, expectedSatoshiOut)
		return ruleError(ErrBadCoinbaseValue, str)
	}
	preHash := block.MsgBlock().Header.PrevBlock
	preHeight := node.height - 1
	if preHeight > 0 {
		if preblock, err := b.blockByHashAndHeight(&preHash, preHeight); err != nil {
			str := fmt.Sprintf("cann't get preblock %v,current height: %d,err:%s",
				preblock, node.height, err.Error())
			return ruleError(ErrPrevBlockNotBest, str)
		} else {
			if err := checkBlockSubsidy(block, preblock, node.height, view,
				amountSubsidy, b.chainParams); err != nil {
				return err
			}
		}
	}

	// Don't run scripts if this node is before the latest known good
	// checkpoint since the validity is verified via the checkpoints (all
	// transactions are included in the merkle root hash and any changes
	// will therefore be detected by the next checkpoint).  This is a huge
	// optimization because running the scripts is the most time consuming
	// portion of block handling.
	checkpoint := b.LatestCheckpoint()
	runScripts := true
	if checkpoint != nil && node.height <= checkpoint.Height {
		runScripts = false
	}

	// Enforce CHECKSEQUENCEVERIFY during all block validation checks once
	// the soft-fork deployment is fully active.
	csvState, err := b.deploymentState(node.parent, chaincfg.DeploymentCSV)
	if err != nil {
		return err
	}
	if csvState == ThresholdActive {
		// If the CSV soft-fork is now active, then modify the
		// scriptFlags to ensure that the CSV op code is properly
		// validated during the script checks bleow.
		scriptFlags |= txscript.ScriptVerifyCheckSequenceVerify

		// We obtain the MTP of the *previous* block in order to
		// determine if transactions in the current block are final.
		medianTime := node.parent.CalcPastMedianTime()

		// Additionally, if the CSV soft-fork package is now active,
		// then we also enforce the relative sequence number based
		// lock-times within the inputs of all transactions in this
		// candidate block.
		for _, tx := range block.Transactions() {
			// A transaction can only be included within a block
			// once the sequence locks of *all* its inputs are
			// active.
			sequenceLock, err := b.calcSequenceLock(node, tx, view,
				false)
			if err != nil {
				return err
			}
			if !SequenceLockActive(sequenceLock, node.height,
				medianTime) {
				str := fmt.Sprintf("block contains " +
					"transaction whose input sequence " +
					"locks are not met")
				return ruleError(ErrUnfinalizedTx, str)
			}
		}
	}

	// Now that the inexpensive checks are done and have passed, verify the
	// transactions are actually allowed to spend the coins by running the
	// expensive ECDSA signature check scripts.  Doing this last helps
	// prevent CPU exhaustion attacks.
	if runScripts {
		err := checkBlockScripts(block, view, scriptFlags, b.sigCache,
			b.hashCache)
		if err != nil {
			return err
		}
	}

	return nil
}

// CheckConnectBlockTemplate fully validates that connecting the passed block to
// the main chain does not violate any consensus rules, aside from the proof of
// work requirement. The block must connect to the current tip of the main chain.
//
// This function is safe for concurrent access.
func (b *BlockChain) CheckConnectBlockTemplate(block *czzutil.Block) error {
	b.chainLock.Lock()
	defer b.chainLock.Unlock()

	// Skip the proof of work check as this is just a block template.
	flags := BFNoPoWCheck

	// This only checks whether the block can be connected to the tip of the
	// current chain.
	tip := b.bestChain.Tip()
	header := block.MsgBlock().Header
	if tip.hash != header.PrevBlock {
		str := fmt.Sprintf("previous block must be the current chain tip %v, "+
			"instead got %v", tip.hash, header.PrevBlock)
		return ruleError(ErrPrevBlockNotBest, str)
	}
	var err error
	// If MagneticAnomaly is active make sure the block sanity is checked using the
	// new rule set.
	flags |= BFMagneticAnomaly

	err = checkBlockSanity(b, block, b.chainParams.PowLimit, b.timeSource, flags)
	if err != nil {
		return err
	}

	err = b.checkBlockContext(block, tip, flags)
	if err != nil {
		return err
	}

	// Leave the spent txouts entry nil in the state since the information
	// is not needed and thus extra work can be avoided.
	view := NewUtxoViewpoint()
	newNode := newBlockNode(&header, tip)
	return b.checkConnectBlock(newNode, block, view, nil)
}

type KeepedInfoSummay struct {
	TotalIn             int64
	TotalOut            int64
	KeepedAmountInBlock cross.KeepedAmount
	EntangleAmount      int64
	lastpool1Amount     int64
	lastpool2Amount     int64
	pool1Amount         int64
	pool2Amount         int64
}

func getKeepedAmountFormPreBlock(block *czzutil.Block) (*cross.KeepedAmount, error) {
	keepInfo := &cross.KeepedAmount{
		Count: 0,
		Items: make([]cross.KeepedItem, 0),
	}
	coinbaseTx, err := block.Tx(0)
	if err == nil {
		if len(coinbaseTx.MsgTx().TxOut) >= 4 {
			if err := keepInfo.Parse(coinbaseTx.MsgTx().TxOut[3].PkScript); err != nil {
				return keepInfo, err
			}
		}
	}
	return keepInfo, nil
}
func getPoolAmountFromPreBlock(block *czzutil.Block, summay *KeepedInfoSummay) error {
	coinbaseTx, err := block.Tx(0)
	if err == nil {
		summay.lastpool1Amount = coinbaseTx.MsgTx().TxOut[1].Value
		summay.lastpool2Amount = coinbaseTx.MsgTx().TxOut[2].Value
	}
	return err
}

func handleSummayEntangle(summay *KeepedInfoSummay, keepedInfo *cross.KeepedAmount, infos map[uint32]*cross.EntangleTxInfo) {
	for _, v := range infos {
		item := &cross.EntangleItem{
			EType: v.ExTxType,
			Value: new(big.Int).Set(v.Amount),
		}
		summay.KeepedAmountInBlock.Add(cross.KeepedItem{
			ExTxType: item.EType,
			Amount:   new(big.Int).Set(item.Value),
		})
		cross.PreCalcEntangleAmount(item, keepedInfo)
		summay.EntangleAmount += item.Value.Int64()
	}
}

func summayOfTxsAndCheck(preblock, block *czzutil.Block, utxoView *UtxoViewpoint, subsidy, pool1Amount, pool2Amount int64) (*KeepedInfoSummay, error) {
	var totalIn, totalOut, amount1 int64
	summay := &KeepedInfoSummay{
		KeepedAmountInBlock: cross.KeepedAmount{
			Count: 0,
			Items: make([]cross.KeepedItem, 0),
		},
	}
	keepInfo, err := getKeepedAmountFormPreBlock(preblock)
	if err != nil {
		return nil, err
	}
	if err := getPoolAmountFromPreBlock(preblock, summay); err != nil {
		return nil, err
	}
	totalIn = summay.lastpool1Amount + summay.lastpool2Amount + pool1Amount + pool2Amount + subsidy
	txs := block.Transactions()
	for txIndex, tx := range txs {
		if txIndex == 0 {
			for i, txout := range tx.MsgTx().TxOut {
				if i > 3 {
					amount1 = amount1 + txout.Value
				}
				if i == 1 {
					summay.pool1Amount = txout.Value
				}
				if i == 2 {
					summay.pool2Amount = txout.Value
				}
				totalOut = totalOut + txout.Value
			}
		} else {
			// summay all txout
			einfos, _ := cross.IsEntangleTx(tx.MsgTx())
			if einfos != nil {
				handleSummayEntangle(summay, keepInfo, einfos)
			}
			for _, txout := range tx.MsgTx().TxOut {
				totalOut = totalOut + txout.Value
			}
			// summay all txin
			for i, txIn := range tx.MsgTx().TxIn {
				utxo := utxoView.LookupEntry(txIn.PreviousOutPoint)
				if utxo == nil {
					str := fmt.Sprintf("output %v referenced from "+
						"transaction %s:%d does not valid", txIn.PreviousOutPoint,
						tx.Hash(), i)
					return nil, ruleError(ErrMissingTxOut, str)
				}
				totalIn = totalIn + utxo.amount
			}
		}
	}
	// check entangle amount
	if amount1 != summay.EntangleAmount {
		return nil, errors.New(fmt.Sprintf("not match the entangle amount.[%v,%v]", amount1, summay.EntangleAmount))
	}
	summay.TotalIn, summay.TotalOut = totalIn, totalOut
	return summay, nil
}

func getPoolAddress(pk []byte, chainParams *chaincfg.Params) (czzutil.Address, error) {
	addr, err := czzutil.NewAddressPubKeyHash(pk, chainParams)
	return addr, err
}
func matchPoolFromUtxo(utxo *UtxoEntry, index int, chainParams *chaincfg.Params) error {
	CoinPool1 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	CoinPool2 := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2}
	var pool []byte
	if index == 1 {
		pool = CoinPool1[:]
	} else if index == 2 {
		pool = CoinPool2[:]
	} else {
		errors.New("wrong index of pool address")
	}
	addr, err := getPoolAddress(pool, chainParams)
	if err != nil {
		return errors.New("[pool not match:]" + err.Error())
	}
	class, addrs, reqSigs, err1 := txscript.ExtractPkScriptAddrs(pool, chainParams)
	if err1 != nil {
		errors.New("[pool not match:]" + err1.Error())
	}
	if class != txscript.PubKeyHashTy || reqSigs != 1 || len(addrs) != 1 ||
		addr.String() != addrs[0].String() {
		return errors.New(fmt.Sprintf("pool not match[class:%v,req:%d,addr=%v]",
			class, reqSigs, addrs))
	}
	return nil
}
func getFee(tx *czzutil.Tx, txHeight int32, utxoView *UtxoViewpoint, chainParams *chaincfg.Params) (int64, error) {
	if isCoinBaseInParam(tx, chainParams) {
		return 0, nil
	}
	txHash := tx.Hash()
	var totalSatoshiIn int64
	for txInIndex, txIn := range tx.MsgTx().TxIn {
		// Ensure the referenced input transaction is available.
		utxo := utxoView.LookupEntry(txIn.PreviousOutPoint)
		if utxo == nil {
			str := fmt.Sprintf("output %v referenced from "+
				"transaction %s:%d does not exist", txIn.PreviousOutPoint,
				tx.Hash(), txInIndex)
			return 0, ruleError(ErrMissingTxOut, str)
		}
		originTxSatoshi := utxo.Amount()
		if originTxSatoshi < 0 {
			str := fmt.Sprintf("transaction output has negative "+
				"value of %v", czzutil.Amount(originTxSatoshi))
			return 0, ruleError(ErrBadTxOutValue, str)
		}
		totalSatoshiIn += originTxSatoshi
	}

	// Calculate the total output amount for this transaction.  It is safe
	// to ignore overflow and out of range errors here because those error
	// conditions would have already been caught by checkTransactionSanity.
	var totalSatoshiOut int64
	for _, txOut := range tx.MsgTx().TxOut {
		totalSatoshiOut += txOut.Value
	}

	// Ensure the transaction does not spend more than its inputs.
	if totalSatoshiIn < totalSatoshiOut {
		str := fmt.Sprintf("total value of all transaction inputs for "+
			"transaction %v is %v which is less than the amount "+
			"spent of %v", txHash, totalSatoshiIn, totalSatoshiOut)
		return 0, ruleError(ErrSpendTooHigh, str)
	}

	// NOTE: bitcoind checks if the transaction fees are < 0 here, but that
	// is an impossible condition because of the check above that ensures
	// the inputs are >= the outputs.
	txFeeInSatoshi := totalSatoshiIn - totalSatoshiOut
	return txFeeInSatoshi, nil
}
func getEtsInfoInBlock(block *czzutil.Block, utxoView *UtxoViewpoint, chainParams *chaincfg.Params) ([]*cross.EtsInfo, error) {

	txs := block.Transactions()
	infos := make([]*cross.EtsInfo, 0)
	height := block.Height()
	for _, tx := range txs {
		fee, err := getFee(tx, height, utxoView, chainParams)
		if err != nil {
			return nil, err
		}
		etsInfo := &cross.EtsInfo{
			FeePerKB: fee * 1000 / int64(tx.MsgTx().SerializeSize()),
			Tx:       tx.MsgTx(),
		}
		infos = append(infos, etsInfo)
	}
	return infos, nil
}
