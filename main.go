package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"log"
	"math/big"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/tyler-smith/go-bip39"
	"gopkg.in/yaml.v2"
)

type Config struct {
	BatchSize   int      `yaml:"batchSize"`
	RateLimit   int      `yaml:"rateLimit"`
	EthChain    bool     `yaml:"ethChain"`
	BscChain    bool     `yaml:"bscChain"`
	EthRpcList  []string `yaml:"ethRpcList"`
	BscRpcList  []string `yaml:"bscRpcList"`
	SendWebhook bool     `yaml:"sendWebhook"`
	Log0Wallet  bool     `yaml:"log0Wallets"`
}

type WebhookData struct {
	Content string         `json:"content"`
	Embeds  []WebhookEmbed `json:"embeds"`
}

type WebhookEmbed struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Image       WImage  `json:"image"`
	Footer      WFooter `json:"footer"`
	Color       int     `json:"color"`
}

type WImage struct {
	URL string `json:"url"`
}

type WFooter struct {
	IconURL string `json:"icon_url"`
	Text    string `json:"text"`
}

type WebhookInfo struct {
	Name  string `json:"name"`
	Owner string `json:"owner"`
	After int    `json:"after"`
}

type Webhook struct {
	Url         string
	Name        string
	Owner       string
	Alive       bool
	Ratelimit   int
	TotalSent   int
	TotalMissed int
}

var webhooks []Webhook
var ThongBaoMessage *WebhookData
var whRegex = regexp.MustCompile(`(?i)^.*(discord|discordapp)\.com\/api\/webhooks\/([\d]+)\/([a-z0-9_-]+)$`)

func init() {
	ThongBaoMessage = &WebhookData{
		Content: "@everyone FOUND WALLET HAVE BALANCE!!!!",
		Embeds: []WebhookEmbed{
			{
				Title:       "wallet_scanner @127.0.0.3107",
				Description: "**Address**: `%address%`\n**Balance**: `%eth% | %bsc%`\n**Seed**: `%seed%`\n**PrivateKey**: `%privatekey%`",
				Image: WImage{
					URL: "https://cdn.discordapp.com/avatars/921245954923987005/5d5c39ac4d55d112633166148486e8a5.png?size=1024",
				},
				Footer: WFooter{
					IconURL: "https://cdn.discordapp.com/avatars/921245954923987005/5d5c39ac4d55d112633166148486e8a5.png?size=1024",
					Text:    "wallet_scanner @127.0.0.3107",
				},
				Color: 14194190,
			},
		},
	}

	mfile, err := os.OpenFile("message.json", os.O_RDWR|os.O_CREATE, fs.ModePerm)
	if err != nil {
		fmt.Println("Failed to read message.json!")
	} else {
		defer mfile.Close()

		mb, err := io.ReadAll(mfile)
		if err != nil {
			panic(err)
		}

		if len(mb) < 10 {
			newone, _ := json.MarshalIndent(ThongBaoMessage, "", " ")
			mfile.Write(newone)
		} else {
			err = json.Unmarshal(mb, ThongBaoMessage)
			if err != nil {
				fmt.Println("Failed to read unmarshal spam message data!")
			}
		}
	}

	wfile, err := os.OpenFile("webhooks.txt", os.O_RDWR, fs.ModePerm)
	if err != nil {
		fmt.Println("Failed to open webhooks.txt!", err)
		return
	}
	defer wfile.Close()

	scanner := bufio.NewScanner(wfile)
	for scanner.Scan() {
		line := scanner.Text()
		if whRegex.MatchString(line) {
			webhooks = append(webhooks, Webhook{Url: line, Alive: true})
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Println("Failed to read webhooks.txt!", err)
		panic(err)
	}

	if len(webhooks) == 0 {
		fmt.Println("No valid webhooks found in file.")
	}
}

func executeWebhookForWallet(walletAddress, ethbalance, bnbbalance, walletPhrase, walletPrivateKey string) error {
	for _, webhook := range webhooks {
		if !webhook.Alive {
			continue
		}

		client := &http.Client{}
		message := *ThongBaoMessage
		for i := range message.Embeds {
			message.Embeds[i].Description = strings.ReplaceAll(message.Embeds[i].Description, "%address%", walletAddress)
			message.Embeds[i].Description = strings.ReplaceAll(message.Embeds[i].Description, "%eth%", ethbalance)
			message.Embeds[i].Description = strings.ReplaceAll(message.Embeds[i].Description, "%bnb%", bnbbalance)
			message.Embeds[i].Description = strings.ReplaceAll(message.Embeds[i].Description, "%seed%", walletPhrase)
			message.Embeds[i].Description = strings.ReplaceAll(message.Embeds[i].Description, "%privatekey%", walletPrivateKey)
		}

		postBody, err := json.Marshal(message)
		if err != nil {
			fmt.Println("Error marshaling JSON:", err)
			continue
		}

		req, err := http.NewRequest("POST", webhook.Url, bytes.NewReader(postBody))
		if err != nil {
			fmt.Println("Error creating POST request:", err)
			continue
		}

		req.Header.Set("User-Agent", "Mozilla/5.0")
		req.Header.Set("Content-Type", "application/json")

		res, err := client.Do(req)
		if err != nil {
			fmt.Println("Error sending POST request:", err)
			continue
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
			bodyBytes, _ := io.ReadAll(res.Body)
			fmt.Printf("Webhook POST failed, status: %d, body: %s\n", res.StatusCode, string(bodyBytes))

			if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusForbidden {
				fmt.Println("Webhook not found or forbidden; marking as inactive")
				webhook.Alive = false
			}
		} else {
			fmt.Println("Webhook POST successful to discord!")
		}

		time.Sleep(44 * time.Millisecond)
	}
	return nil
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
	if len(apiKeys) == 0 {
		return ""
	}
	provider := apiKeys[*currentProviderIndex]
	*currentProviderIndex = (*currentProviderIndex + 1) % len(apiKeys)
	return provider
}

func GenWallet(mode string) (string, string, string, error) {
	switch mode {
	case "randomprivatekey":
		privateKey, err := crypto.GenerateKey()
		if err != nil {
			return "", "", "", err
		}
		address := crypto.PubkeyToAddress(privateKey.PublicKey).Hex()
		privateKeyBytes := crypto.FromECDSA(privateKey)
		privateKeyHex := hex.EncodeToString(privateKeyBytes)
		return address, "", privateKeyHex, nil
	case "random12seed":
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
		privateKeyHex := hexutil.Encode(crypto.FromECDSA(privateKey))[2:]
		return address, mnemonic, privateKeyHex, nil
	case "test":
		privatekeytest := "0000000000000000000000000000000000000000000000000000000000000013"

		privateKeyBytes, err := hex.DecodeString(privatekeytest)
		if err != nil {
			return "", "", "", err
		}

		privateKey, err := crypto.ToECDSA(privateKeyBytes)
		if err != nil {
			return "", "", "", err
		}

		address := crypto.PubkeyToAddress(privateKey.PublicKey).Hex()
		privateKeyHex := hex.EncodeToString(privateKeyBytes)
		return address, "", privateKeyHex, nil
	default:
		return "", "", "", fmt.Errorf("unsupported mode: %s", mode)
	}
}

func BatchWallets(batchSize int, mode string) ([]string, []string, []string, error) {
	addresses := make([]string, batchSize)
	mnemonics := make([]string, batchSize)
	privateKeys := make([]string, batchSize)

	for i := 0; i < batchSize; i++ {
		address, mnemonic, privateKey, err := GenWallet(mode)
		if err != nil {
			return nil, nil, nil, err
		}
		addresses[i] = address
		mnemonics[i] = mnemonic
		privateKeys[i] = privateKey
	}

	return addresses, mnemonics, privateKeys, nil
}

func checkBalances(ethClient *rpc.Client, bscClient *rpc.Client, addresses []string, ethChain, bscChain bool) ([]*big.Float, []*big.Float, error) {
	batchSize := len(addresses)
	ethBalances := make([]*big.Float, batchSize)
	bscBalances := make([]*big.Float, batchSize)

	if ethChain {
		batchElemsEth := make([]rpc.BatchElem, batchSize)
		for i, address := range addresses {
			batchElemsEth[i] = rpc.BatchElem{
				Method: "eth_getBalance",
				Args:   []interface{}{address, "latest"},
				Result: new(string),
			}
		}
		err := ethClient.BatchCallContext(context.Background(), batchElemsEth)
		if err != nil {
			return nil, nil, err
		}
		for i, elem := range batchElemsEth {
			if elem.Error != nil {
				return nil, nil, elem.Error
			}
			balanceStr := *(elem.Result.(*string))
			balance := new(big.Int)
			balance.SetString(balanceStr[2:], 16)
			ethBalances[i] = new(big.Float).Quo(new(big.Float).SetInt(balance), big.NewFloat(1e18))
		}
	} else {
		for i := range ethBalances {
			ethBalances[i] = big.NewFloat(0)
		}
	}

	if bscChain {
		batchElemsBsc := make([]rpc.BatchElem, batchSize)
		for i, address := range addresses {
			batchElemsBsc[i] = rpc.BatchElem{
				Method: "eth_getBalance",
				Args:   []interface{}{address, "latest"},
				Result: new(string),
			}
		}
		err := bscClient.BatchCallContext(context.Background(), batchElemsBsc)
		if err != nil {
			return nil, nil, err
		}
		for i, elem := range batchElemsBsc {
			if elem.Error != nil {
				return nil, nil, elem.Error
			}
			balanceStr := *(elem.Result.(*string))
			balance := new(big.Int)
			balance.SetString(balanceStr[2:], 16)
			bscBalances[i] = new(big.Float).Quo(new(big.Float).SetInt(balance), big.NewFloat(1e18))
		}
	} else {
		for i := range bscBalances {
			bscBalances[i] = big.NewFloat(0)
		}
	}

	return ethBalances, bscBalances, nil
}

func FormatBalance(balance *big.Float) string {
	balanceStr := balance.Text('f', 18)
	return strings.TrimRight(strings.TrimRight(balanceStr, "0"), ".")
}

func ProcessBatch(batchSize int, mode string, ethRpcList, bscRpcList []string, currentProviderIndex *int, config Config) error {
	ethProviderURL := RandomProvider(ethRpcList, currentProviderIndex)
	bscProviderURL := RandomProvider(bscRpcList, currentProviderIndex)
	ethClient, err := rpc.DialContext(context.Background(), ethProviderURL)
	if err != nil {
		return err
	}
	defer ethClient.Close()

	bscClient, err := rpc.DialContext(context.Background(), bscProviderURL)
	if err != nil {
		return err
	}
	defer bscClient.Close()

	addresses, mnemonics, privateKeys, err := BatchWallets(batchSize, mode)
	if err != nil {
		return err
	}

	ethBalances, bscBalances, err := checkBalances(ethClient, bscClient, addresses, config.EthChain, config.BscChain)
	if err != nil {
		return err
	}

	for i, ethBalance := range ethBalances {
		bscBalance := bscBalances[i]
		ethBalanceStr := FormatBalance(ethBalance)
		bnbBalanceStr := FormatBalance(bscBalance)

		if ethBalance.Cmp(big.NewFloat(0)) > 0 || bscBalance.Cmp(big.NewFloat(0)) > 0 {
			var entry string
			if ethBalance.Cmp(big.NewFloat(0)) > 0 && bscBalance.Cmp(big.NewFloat(0)) > 0 {
				entry = fmt.Sprintf("✅ %s | ETH: %s | BNB: %s | %s | %s | %s\n", addresses[i], ethBalanceStr, bnbBalanceStr, mnemonics[i], privateKeys[i], mode)
			} else if ethBalance.Cmp(big.NewFloat(0)) > 0 {
				entry = fmt.Sprintf("✅ %s | ETH: %s | BNB: OwO | %s | %s | %s\n", addresses[i], ethBalanceStr, mnemonics[i], privateKeys[i], mode)
			} else {
				entry = fmt.Sprintf("✅ %s | ETH: OwO | BNB: %s | %s | %s | %s\n", addresses[i], bnbBalanceStr, mnemonics[i], privateKeys[i], mode)
			}

			fmt.Print(entry)
			file, err := os.OpenFile("result.txt", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
			if err != nil {
				fmt.Println("Failed to open result file:", err)
				return err
			}
			defer file.Close()

			if _, err := file.WriteString(entry); err != nil {
				fmt.Println("Failed to write to result file:", err)
				return err
			}

			if config.SendWebhook {
				err := executeWebhookForWallet(addresses[i], ethBalanceStr, bnbBalanceStr, mnemonics[i], privateKeys[i])
				if err != nil {
					fmt.Println("Failed to send webhook:", err)
				}
			}
		} else {
			entry := fmt.Sprintf("❌ %s | ETH: %s | BSC: %s | %s | %s\n", addresses[i], ethBalanceStr, bnbBalanceStr, privateKeys[i], mode)
			fmt.Print(entry)
			if config.Log0Wallet {
				file, err := os.OpenFile("0wallets.txt", os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
				if err != nil {
					fmt.Println("Failed to open 0wallets file:", err)
					return err
				}
				defer file.Close()

				if _, err := file.WriteString(entry); err != nil {
					fmt.Println("Failed to write to 0wallets file:", err)
					return err
				}
			}
		}
		totalChecked++
	}

	return nil
}

func RetryCheckBalance(batchSize int, retries int, mode string, ethRpcList []string, bscRpcList []string, currentProviderIndex *int, config Config, wg *sync.WaitGroup) {
	defer wg.Done()
	for attempt := 0; attempt < retries; attempt++ {
		err := ProcessBatch(batchSize, mode, ethRpcList, bscRpcList, currentProviderIndex, config)
		if err == nil {
			return
		}
		fmt.Printf("Error: %s. Retrying in %d seconds...\n", err.Error(), 1<<attempt)
		time.Sleep(time.Duration(1<<attempt) * time.Second)
	}
	fmt.Println("Failed after multiple retries.")
}

var mode string

func init() {
	flag.StringVar(&mode, "mode", "random12seed", "Mode Support: randomprivatekey or random12seed")
	flag.Parse()
}
func main() {
	config, err := loadConfig("config.yaml")
	if err != nil {
		log.Fatalf("error loading config: %v", err)
	}

	fmt.Println("Supported Chains:")
	if config.EthChain {
		fmt.Println("- Ethereum")
	}
	if config.BscChain {
		fmt.Println("- Binance Smart Chain")
	}
	fmt.Println("")

	fmt.Println("Waiting 5 sec...")
	time.Sleep(5 * time.Second)

	walletsPerCycle := config.BatchSize * config.RateLimit
	fmt.Println("PerCycle:", walletsPerCycle)
	var wg sync.WaitGroup
	CurrentProvider := 0

	for {
		for i := 0; i < config.RateLimit; i++ {
			wg.Add(1)
			go RetryCheckBalance(config.BatchSize, 5, mode, config.EthRpcList, config.BscRpcList, &CurrentProvider, config, &wg)
		}
		wg.Wait()
		fmt.Printf("Total wallets checked: %d\n", totalChecked)
	}
}
