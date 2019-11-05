package main

import (
	"context"
	"database/sql"
	"fmt"

	//"github.com/HydroProtocol/hydro-scaffold-dex/backend/cli"
	"github.com/HydroProtocol/hydro-scaffold-dex/backend/cli"
	"github.com/HydroProtocol/hydro-scaffold-dex/backend/models"
	"github.com/HydroProtocol/hydro-sdk-backend/launcher"
	"github.com/HydroProtocol/hydro-sdk-backend/sdk/ethereum"
	"github.com/HydroProtocol/hydro-sdk-backend/utils"

	//"github.com/HydroProtocol/hydro-sdk-backend/launcher"
	//"github.com/HydroProtocol/hydro-sdk-backend/sdk/ethereum"
	//"github.com/HydroProtocol/hydro-sdk-backend/utils"
	_ "github.com/joho/godotenv/autoload"
	"github.com/shopspring/decimal"

	"github.com/klaytn/klaytn/blockchain/types"
	"github.com/klaytn/klaytn/client"
	"github.com/klaytn/klaytn/common"
	"github.com/klaytn/klaytn/crypto"

	"os"
	"time"

	// "encoding/json"
	// "bytes"
	// "net/http"
	// "io/ioutil"

	"math/big"
)

func run() int {
	ctx, stop := context.WithCancel(context.Background())
	go cli.WaitExitSignal(stop)

	models.Connect(os.Getenv("HSK_DATABASE_URL"))

	// blockchain
	hydro := ethereum.NewEthereumHydro(os.Getenv("HSK_BLOCKCHAN_RPC_URL"), os.Getenv("HSK_HYBRID_EXCHANGE_ADDRESS"))
	if os.Getenv("HSK_LOG_LEVEL") == "DEBUG" {
		hydro.EnableDebug(true)
	}

	signService := launcher.NewDefaultSignService(os.Getenv("HSK_RELAYER_PK"), hydro.GetTransactionCount)

	fallbackGasPrice := decimal.New(3, 9) // 3Gwei
	priceDecider := launcher.NewGasStationGasPriceDecider(fallbackGasPrice)

	launcher := launcher.NewLauncher(ctx, signService, hydro, priceDecider)

	Run(launcher, utils.StartMetrics)

	return 0
}

const pollingIntervalSeconds = 5

func Run(l *launcher.Launcher, startMetrics func()) {
	utils.Infof("launcher start!")
	defer utils.Infof("launcher stop!")
	go startMetrics()

	for {
		launchLogs := models.LaunchLogDao.FindAllCreated()

		if len(launchLogs) == 0 {
			select {
			case <-l.Ctx.Done():
				utils.Infof("main loop Exit")
				return
			default:
				utils.Infof("no logs need to be sent. sleep %ds", pollingIntervalSeconds)

				time.Sleep(pollingIntervalSeconds * time.Second)
				continue
			}
		}

		for _, modelLaunchLog := range launchLogs {
			modelLaunchLog.GasPrice = decimal.NullDecimal{
				Decimal: l.GasPriceDecider.GasPriceInWei(),
				Valid:   true,
			}

			log := launcher.LaunchLog{
				ID:          modelLaunchLog.ID,
				ItemType:    modelLaunchLog.ItemType,
				ItemID:      modelLaunchLog.ItemID,
				Status:      modelLaunchLog.Status,
				Hash:        modelLaunchLog.Hash,
				BlockNumber: modelLaunchLog.BlockNumber,
				From:        modelLaunchLog.From,
				To:          modelLaunchLog.To,
				Value:       modelLaunchLog.Value,
				GasLimit:    modelLaunchLog.GasLimit,
				GasUsed:     modelLaunchLog.GasUsed,
				GasPrice:    modelLaunchLog.GasPrice,
				Nonce:       modelLaunchLog.Nonce,
				Data:        modelLaunchLog.Data,
				ExecutedAt:  modelLaunchLog.ExecutedAt,
				CreatedAt:   modelLaunchLog.CreatedAt,
				UpdatedAt:   modelLaunchLog.UpdatedAt,
			}

			client, err := client.Dial("https://api.cypress.klaytn.net:8651")
			if err != nil {
				panic(err)
			}

			//payload, _ := json.Marshal(launchLog)
			//json.Unmarshal(payload, &log)
			privateKey, err := crypto.HexToECDSA("AE321B87A3AFF2224CC87020516D1ACDD50EB0A65EF8F7925CBEE024F7AB6852")
			if err != nil {
				panic(err)
			}

			gasPrice, err := client.SuggestGasPrice(context.Background())
			if err != nil {
				panic(err)
			}
			nonce, err := client.PendingNonceAt(context.Background(), common.HexToAddress("0x9150999f42A643e0AAd1e358d74F26B6e8d56F86"))
			if err != nil {
				panic(err)
			}

			//balance, err := client.BalanceAt(context.Background(), common.HexToAddress("0x9150999f42A643e0AAd1e358d74F26B6e8d56F86"), nil)
			//fmt.Println("balance", balance.String())
			value := big.NewInt(0)
			gasLimit := uint64(3000000)
			toAddress := common.HexToAddress(os.Getenv("HSK_HYBRID_EXCHANGE_ADDRESS"))
			data := common.Hex2Bytes(log.Data[2:])

			//tx := types.NewTransaction(nonce, toAddress, value, gasLimit, gasPrice, data)
			feePayerPrv, err := crypto.HexToECDSA("93d501286bc90f2271340a97432801e4406a9b8a7f4ab006f97cd426796668d8")
			tx, err := types.NewTransactionWithMap(types.TxTypeFeeDelegatedSmartContractExecution, map[types.TxValueKeyType]interface{}{
				types.TxValueKeyNonce:    nonce,
				types.TxValueKeyTo:       toAddress,
				types.TxValueKeyAmount:   value,
				types.TxValueKeyGasLimit: gasLimit,
				types.TxValueKeyGasPrice: gasPrice,
				types.TxValueKeyFrom:     common.HexToAddress("0x9150999f42A643e0AAd1e358d74F26B6e8d56F86"),
				types.TxValueKeyData:     data,
				types.TxValueKeyFeePayer: common.HexToAddress("0xad7c07eab1e56fbbb976fd5377e5088ec5528cd9"),
			})

			//tx := &types.Transaction{data: internalTx}
			chainID, err := client.NetworkID(context.Background())
			if err != nil {
				panic(err)
			}
			fmt.Println("gasPrice", gasPrice, chainID.String(), privateKey)

			signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
			signedTx.SignFeePayer(types.NewEIP155Signer(chainID), feePayerPrv)
			hash, err := client.SendRawTransaction(context.Background(), signedTx)

			if err != nil {
				utils.Debugf("%+v", modelLaunchLog)
				fmt.Println("Send Tx failed, launchLog ID: %d, err: %+v", modelLaunchLog.ID, err)
				panic(err)
			}
			fmt.Println(hash.Hex())
			utils.Infof("Send Tx, launchLog ID: %d, hash: %s", modelLaunchLog.ID, hash.Hex())

			// todo any other fields?
			modelLaunchLog.Hash = sql.NullString{
				hash.Hex(),
				true,
			}

			models.UpdateLaunchLogToPending(modelLaunchLog)

			if err != nil {
				utils.Infof("Update Launch Log Failed, ID: %d, err: %s", modelLaunchLog.ID, err)
				panic(err)
			}

			l.SignService.AfterSign()
		}
	}
}

func main() {
	os.Exit(run())
}
