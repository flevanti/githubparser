// main.go
package main

import (
	"fmt"
	"github.com/aws/aws-lambda-go/lambda"
	"io/ioutil"
	"log"
	"encoding/json"
	"os"
	"bufio"
	"time"
	"strconv"
	"strings"
	"errors"
)

var isAWS bool
var metadata map[string]string //we could add {} at the end to initialise the map...
var rules []Rule
var rulesOK int
var rulesKO int
var rulesResultOK []RuleResult
var rulesResultKO []RuleResult
var projrootprefix = "[PROOT]"
var configFileName = "config"
var dummyPayloadFileName = "payload"
var receiptLogLevel = 4
var receipt []Receipt

type Rule struct {
	allowed bool
	path    string
}
type Receipt struct {
	level   int
	message string
}
type RuleResult struct {
	path         string
	allowed      bool
	rulesApplied []string
}

// this is the structure of the github webhook payload
// element not needed are removed to use less memory
// structure obtained thanks to
//
// http://json2struct.mervine.net/
type Request struct {
	Commits []struct {
		Added []string `json:"added"`
		Author struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		} `json:"author"`
		Committer struct {
			Email    string `json:"email"`
			Name     string `json:"name"`
			Username string `json:"username"`
		} `json:"committer"`
		Distinct  bool     `json:"distinct"`
		ID        string   `json:"id"`
		Message   string   `json:"message"`
		Modified  []string `json:"modified"`
		Removed   []string `json:"removed"`
		Timestamp string   `json:"timestamp"`
		TreeID    string   `json:"tree_id"`
		URL       string   `json:"url"`
	} `json:"commits"`
}

func main() {
	checkIfAWS()
	if isAWS {
		lambda.Start(Handler)
	} else {
		//printEnvVars()
		request := loadDummyPayload()
		response, err := Handler(request)
		if err == nil {
			fmt.Print(response + "\n")
		}

	}
}

func Handler(request Request) (string, error) {
	//initialise
	metadata = make(map[string]string)
	greetings()
	loadConfig()
	processRequest(request)
	return "", nil
}

func processRequest(request Request) (error) {
	if len(request.Commits) > 0 {
		addToReceipt(strconv.Itoa(len(request.Commits))+" commits found in the payload", 1)
	}

	for k, commit := range request.Commits {
		filesChanged := append(commit.Added, commit.Modified...)
		filesChanged = append(filesChanged, commit.Removed...)
		e("Processing commit #" + strconv.Itoa(k) + "  " + commit.ID)
		e(strconv.Itoa(len(filesChanged)) + " files to process")
	}

	return nil
}

func loadDummyPayload() (Request) {
	var content string
	var request Request
	content = os.Getenv("AWS_LAMBDA_EVENT_BODY")
	if content == "" {
		content = "{}"
	}
	_ = json.Unmarshal([]byte(content), &request)
	return request
}

func loadDummyPayloadFile() (Request) {
	if fileExists(dummyPayloadFileName) {
		var request Request

		content, err := ioutil.ReadFile(dummyPayloadFileName)
		if err != nil {
			log.Fatal(err)
		}
		_ = json.Unmarshal(content, &request)
		e("Dummy payload loaded")
		return request
	}
	panic("DUMMY PAYLOAD FILE NOT FOUND")
}

func fileExists(file string) (bool) {
	if _, err := os.Stat(file); err == nil {
		return true
	}
	return false
}

func loadConfig() (error) {
	addToReceipt("Reading config file ["+configFileName+"]", 2)
	var line string
	var c int
	var prefix string
	var err error
	if !fileExists(configFileName) {
		return errors.New("config file not found")
	}
	fileHandle, _ := os.Open(configFileName)
	defer fileHandle.Close()
	fileScanner := bufio.NewScanner(fileHandle)
	for fileScanner.Scan() {
		c++
		line = fileScanner.Text()
		addToReceipt("Importing line  #"+strconv.Itoa(c)+"  ["+line+"]", 4)
		if len(line) < 3 {
			addToReceipt("Line too short, considered empty. Skipped", 4)
			continue
		}
		prefix = line[0:3]
		line = strings.TrimSpace(line[3:])
		switch prefix {
		case "MDT":
			err = loadConfigMetadata(line)
		case "OKK", "KOO":
			err = loadConfigRule(line, prefix == "OKK")
			break
		case "###", "///", "---":
			addToReceipt("Line is a comment, skipped", 4)
			break
		default:
			addToReceipt("Prefix not valid, skipped", 4)
		} //end switch
		if err != nil {
			return err
		}
	} //end Scan loop

	addToReceipt("Configuration file loaded: "+
		strconv.Itoa(rulesOK)+ " OK rules, "+
		strconv.Itoa(rulesKO)+ " KO rules, "+
		strconv.Itoa(len(metadata))+ " metadata", 1)
	return nil
}

func loadConfigRule(line string, isOKK bool) (error) {
	if isOKK {
		rulesOK++
	} else {
		rulesKO++
	}
	rule := new(Rule)
	rule.allowed = isOKK
	if line[0:1] == "/" {
		line = projrootprefix + line
	}
	rule.path = line
	rules = append(rules, *rule)
	addToReceipt("rule ["+line+"] is allowed ["+strconv.FormatBool(isOKK)+"]", 4)
	return nil
}

func loadConfigMetadata(line string) (error) {
	index := strings.Index(line, "=")
	if index < 0 {
		addToReceipt("Unable to find [=] assignment in metadata element", 4)
		return errors.New("metadata line bad syntax, missing assignment operator")
	}

	key := strings.TrimSpace(line[:index])
	value := strings.TrimSpace(line[index:])
	if len(key) == 0 {
		addToReceipt("unable to find key in metadata element", 4)
		return errors.New("metadata line bad syntax, key is empty")
	}
	if len(value) == 0 {
		addToReceipt("unable to find value in metadata element", 4)
		return errors.New("metadata line bad syntax, value is empty")
	}
	addToReceipt("element ["+key+"] added to metadata with value ["+value+"]", 4)
	metadata[key] = value

	//update known parameters...
	if key == "verboselevel" {
		valueToInt, _ := strconv.Atoi(value)
		addToReceipt("updating receipt log level to "+strconv.Itoa(valueToInt), 4)
		receiptLogLevel = valueToInt
	}

	return nil
}

func greetings() {
	if isAWS {
		e("Hello Jeff")
	} else {
		e("Greetings Professor Falken")
	}
}

func checkIfAWS() {
	if len(os.Getenv("AWS_REGION")) != 0 {
		isAWS = true
	} else {
		isAWS = false
	}
}

func addToReceipt(line string, verboseLevel int) {

	if verboseLevel <= receiptLogLevel {
		receiptRecord := new(Receipt)
		receiptRecord.level = verboseLevel
		receiptRecord.message = line
		receipt = append(receipt, *receiptRecord)
	}
	e(line + "  [RECEIPT]")
}

func e(line string) {
	fmt.Println(getTD() + "  " + line)
}

func getTD() (string) {
	// time date formatting...
	// https://golang.org/src/time/format.go
	return time.Now().Format("2006-01-02 15:04:05.0000")
}

func printEnvVars() {
	for _, pair := range os.Environ() {
		fmt.Println(pair)
	}
}
