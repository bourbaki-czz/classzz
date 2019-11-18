package cross

import (
	"errors"
	"fmt"
	"github.com/bourbaki-czz/classzz/chaincfg"
	"github.com/bourbaki-czz/classzz/txscript"
	"github.com/bourbaki-czz/czzutil"
	"math/big"
	"math/rand"

	"github.com/bourbaki-czz/classzz/rpcclient"
	"github.com/bourbaki-czz/classzz/wire"
)

var (
	ErrHeightTooClose = errors.New("the block heigth to close for entangling")
)

const (
	dogePoolAddr = "DNGzkoZbnVMihLTMq8M1m7L62XvN3d2cN2"
	ltcPoolAddr  = "MUy9qiaLQtaqmKBSk27FXrEEfUkRBeddCZ"
	dogeMaturity = 14
	ltcMaturity  = 14
)

type EntangleVerify struct {
	DogeCoinRPC []*rpcclient.Client
	LtcCoinRPC  []*rpcclient.Client
	Cache       *CacheEntangleInfo
}

func (ev *EntangleVerify) VerifyEntangleTx(tx *wire.MsgTx) ([]*TuplePubIndex, error) {
	/*
		1. check entangle tx struct
		2. check the repeat tx
		3. check the correct tx
		4. check the pool reserve enough reward
	*/
	einfos, _ := IsEntangleTx(tx)
	if einfos == nil {
		return nil, errors.New("not entangle tx")
	}
	pairs := make([]*TuplePubIndex, 0)
	amount := int64(0)
	if ev.Cache != nil {
		for i, v := range einfos {
			if ok := ev.Cache.FetchEntangleUtxoView(v); ok {
				errStr := fmt.Sprintf("[txid:%v, height:%v]", v.ExtTxHash, v.Height)
				return nil, errors.New("txid has already entangle:" + errStr)
			}
			amount += tx.TxOut[i].Value
		}
	}

	for i, v := range einfos {
		if pub, err := ev.verifyTx(v.ExTxType, v.ExtTxHash, v.Index, v.Height, v.Amount); err != nil {
			errStr := fmt.Sprintf("[txid:%v, height:%v]", v.ExtTxHash, v.Index)
			return nil, errors.New("txid verify failed:" + errStr + " err:" + err.Error())
		} else {
			pairs = append(pairs, &TuplePubIndex{
				EType: v.ExTxType,
				Index: i,
				Pub:   pub,
			})
		}
	}

	// find the pool addrees
	// reserve := GetPoolAmount()
	// if amount >= reserve {
	// 	e := fmt.Sprintf("amount not enough,[request:%v,reserve:%v]", amount, reserve)
	// 	return errors.New(e),nil
	// }
	return pairs, nil
}

func (ev *EntangleVerify) verifyTx(ExTxType ExpandedTxType, ExtTxHash []byte, Vout uint32,
	height uint64, amount *big.Int) ([]byte, error) {
	switch ExTxType {
	case ExpandedTxEntangle_Doge:
		return ev.verifyDogeTx(ExtTxHash, Vout, amount, height)
	case ExpandedTxEntangle_Ltc:
		return ev.verifyLtcTx(ExtTxHash, Vout, amount, height)
	}
	return nil, nil
}

func (ev *EntangleVerify) verifyDogeTx(ExtTxHash []byte, Vout uint32, Amount *big.Int, height uint64) ([]byte, error) {

	// Notice the notification parameter is nil since notifications are
	// not supported in HTTP POST mode.
	client := ev.DogeCoinRPC[rand.Intn(len(ev.DogeCoinRPC))]

	// Get the current block count.
	if tx, err := client.GetRawTransaction(string(ExtTxHash)); err != nil {
		return nil, err
	} else {
		if len(tx.MsgTx().TxOut) < int(Vout) {
			return nil, errors.New("doge TxOut index err")
		}
		if tx.MsgTx().TxOut[Vout].Value != Amount.Int64() {
			e := fmt.Sprintf("amount err ,[request:%v,doge:%v]", Amount, tx.MsgTx().TxOut[Vout].Value)
			return nil, errors.New(e)
		}
		if txscript.GetScriptClass(tx.MsgTx().TxOut[Vout].PkScript) != 2 {
			e := fmt.Sprintf("doge PkScript err")
			return nil, errors.New(e)
		}

		dogeparams := &chaincfg.Params{
			LegacyScriptHashAddrID: 0x1e,
		}

		_, pub, err := txscript.ExtractPkScriptPub(tx.MsgTx().TxOut[Vout].PkScript)
		if err != nil {
			return nil, err
		}

		addr, err := czzutil.NewLegacyAddressScriptHash(pub, dogeparams)
		if err != nil {
			e := fmt.Sprintf("doge Pool err")
			return nil, errors.New(e)
		}

		fmt.Print(addr.String())
		if addr.String() != dogePoolAddr {
			e := fmt.Sprintf("doge dogePoolPub err")
			return nil, errors.New(e)
		}

		if pk, err := txscript.ComputePk(tx.MsgTx().TxIn[0].SignatureScript); err != nil {
			e := fmt.Sprintf("doge PkScript err %s", err)
			return nil, errors.New(e)
		} else {

			if count, err := client.GetBlockCount(); err != nil {
				return nil, err
			} else {
				fmt.Println("pk.Script()", pk)
				if count-int64(height) > dogeMaturity {
					//return nil, pk.Script()[3:23]
					return pk, nil
				} else {
					e := fmt.Sprintf("dogeMaturity err")
					return nil, errors.New(e)
				}
			}
		}
	}
}

func (ev *EntangleVerify) verifyLtcTx(ExtTxHash []byte, Vout uint32, Amount *big.Int, height uint64) ([]byte, error) {

	// Notice the notification parameter is nil since notifications are
	// not supported in HTTP POST mode.
	client := ev.LtcCoinRPC[rand.Intn(len(ev.LtcCoinRPC))]

	// Get the current block count.
	if tx, err := client.GetRawTransaction(string(ExtTxHash)); err != nil {
		return nil, err
	} else {
		if len(tx.MsgTx().TxOut) < int(Vout) {
			return nil, errors.New("ltc TxOut index err")
		}
		if tx.MsgTx().TxOut[Vout].Value != Amount.Int64() {
			e := fmt.Sprintf("amount err ,[request:%v,ltc:%v]", Amount, tx.MsgTx().TxOut[Vout].Value)
			return nil, errors.New(e)
		}
		if txscript.GetScriptClass(tx.MsgTx().TxOut[Vout].PkScript) != 2 {
			e := fmt.Sprintf("ltc PkScript err")
			return nil, errors.New(e)
		}

		_, pub, err := txscript.ExtractPkScriptPub(tx.MsgTx().TxOut[Vout].PkScript)
		if err != nil {
			return nil, err
		}

		ltcparams := &chaincfg.Params{
			LegacyScriptHashAddrID: 0x32,
		}
		addr, err := czzutil.NewLegacyAddressScriptHash(pub, ltcparams)
		if err != nil {
			e := fmt.Sprintf("ltcaddr err")
			return nil, errors.New(e)
		}

		fmt.Print(addr.String())
		if addr.String() != ltcPoolAddr {
			e := fmt.Sprintf("ltc ltcPoolAddr err")
			return nil, errors.New(e)
		}

		if pk, err := txscript.ComputePk(tx.MsgTx().TxIn[0].SignatureScript); err != nil {
			e := fmt.Sprintf("ltc PkScript err %s", err)
			return nil, errors.New(e)
		} else {
			if count, err := client.GetBlockCount(); err != nil {
				return nil, err
			} else {
				if count-int64(height) > ltcMaturity {
					return pk, nil
				} else {
					e := fmt.Sprintf("ltcMaturity err")
					return nil, errors.New(e)
				}
			}
		}
	}
}
