package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"math/big"
	"os"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/tyler-smith/go-bip39"
	"gopkg.in/yaml.v2"
)

type Config struct {
	BatchSize int      `yaml:"batchSize"`
	RateLimit int      `yaml:"rateLimit"`
	RpcList   []string `yaml:"rpclist"`
}

func loadConfig(filename string) (Config, error) {
	var config Config
	yamlFile, err := ioutil.ReadFile(filename)
	if err != nil {
		return config, err
	}
	err = yaml.Unmarshal(yamlFile, &config)
	if err != nil {
		return config, err
	}
	return config, nil
}

var totalChecked int

func RandomProvider(apiKeys []string, currentProviderIndex *int) string {
	provider := apiKeys[*currentProviderIndex]
	*currentProviderIndex = (*currentProviderIndex + 1) % len(apiKeys)
	return provider
}

func GenWallet() (string, string, string, error) {
	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		return "", "", "", err
	}
	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return "", "", "", err
	}

	seed := bip39.NewSeed(mnemonic, "")
	privateKey, err := crypto.ToECDSA(seed[:32])
	if err != nil {
		return "", "", "", err
	}
	address := crypto.PubkeyToAddress(privateKey.PublicKey).Hex()

	return address, mnemonic, privateKey.D.String(), nil
}

func BatchWallets(batchSize int) ([]string, []string, error) {
	addresses := make([]string, batchSize)
	mnemonics := make([]string, batchSize)

	for i := 0; i < batchSize; i++ {
		address, mnemonic, _, err := GenWallet()
		if err != nil {
			return nil, nil, err
		}
		addresses[i] = address
		mnemonics[i] = mnemonic
	}

	return addresses, mnemonics, nil
}

func checkBalances(client *rpc.Client, addresses []string) ([]*big.Float, error) {
	batchSize := len(addresses)
	batchElems := make([]rpc.BatchElem, batchSize)

	for i, address := range addresses {
		batchElems[i] = rpc.BatchElem{
			Method: "eth_getBalance",
			Args:   []interface{}{address, "latest"},
			Result: new(string),
		}
	}

	err := client.BatchCallContext(context.Background(), batchElems)
	if err != nil {
		return nil, err
	}

	balances := make([]*big.Float, batchSize)
	for i, elem := range batchElems {
		if elem.Error != nil {
			return nil, elem.Error
		}
		balanceStr := *(elem.Result.(*string))
		balance := new(big.Int)
		balance.SetString(balanceStr[2:], 16)
		balances[i] = new(big.Float).Quo(new(big.Float).SetInt(balance), big.NewFloat(1e18))
	}

	return balances, nil
}

func ProcessBatch(batchSize int, apiKeys []string, currentProviderIndex *int) error {
	providerURL := RandomProvider(apiKeys, currentProviderIndex)
	client, err := rpc.DialContext(context.Background(), providerURL)
	if err != nil {
		return err
	}
	defer client.Close()

	addresses, mnemonics, err := BatchWallets(batchSize)
	if err != nil {
		return err
	}

	balances, err := checkBalances(client, addresses)
	if err != nil {
		return err
	}

	for i, balance := range balances {
		if balance.Cmp(big.NewFloat(0)) > 0 {
			entry := fmt.Sprintf("✅ %s | %s | %s\n", addresses[i], balance.String(), mnemonics[i])
			fmt.Print(entry)
			if err := ioutil.WriteFile("result.txt", []byte(entry), os.ModeAppend); err != nil {
				return err
			}
		} else {
			fmt.Printf("❌ %s | %s\n", addresses[i], balance.String())
		}
		totalChecked++
	}

	return nil
}

func RetryCheckBalance(batchSize, retries int, apiKeys []string, currentProviderIndex *int, wg *sync.WaitGroup) {
	defer wg.Done()
	for attempt := 0; attempt < retries; attempt++ {
		err := ProcessBatch(batchSize, apiKeys, currentProviderIndex)
		if err == nil {
			return
		}
		fmt.Printf("Error: %s. Retrying in %d seconds...\n", err.Error(), 1<<attempt)
		time.Sleep(time.Duration(1<<attempt) * time.Second)
	}
	fmt.Println("Failed after multiple retries.")
}

func main() {
	config, err := loadConfig("config.yml")
	if err != nil {
		log.Fatalf("error loading config: %v", err)
	}

	BatchSize := config.BatchSize
	RateLimit := config.RateLimit
	RpcClient := config.RpcList

	walletsPerCycle := BatchSize * RateLimit
	fmt.Println("PerCycle:", walletsPerCycle)
	var wg sync.WaitGroup
	CurrentProvider := 0
	for {
		for i := 0; i < RateLimit; i++ {
			wg.Add(1)
			go RetryCheckBalance(BatchSize, 5, RpcClient, &CurrentProvider, &wg)
		}
		wg.Wait()
		fmt.Printf("Total wallets checked: %d\n", totalChecked)
	}
}
