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
var rulesNA int
var rulesResults []RuleResult
var projrootprefix = "[PROOT]"
var configFileName = "config"
var dummyPayloadFileName = "payload"
var receiptLogLevel = 4
var receipt []Receipt

type Rule struct {
	allowed      int
	path         string
	originalpath string
}
type Receipt struct {
	level   int
	message string
}
type RuleResult struct {
	allowed      int
	path         string
	originalpath string
	rulesApplied []Rule
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

	//loop through commits....
	for k, commit := range request.Commits {
		// merge all changed (new/updated/removed) files into one element
		// we can merge only 2 elements.. se to merge 3 elements we need to do it twice
		// example... elements A B C
		// TOT = A+B (two elements)
		// TOT = TOT + C (add the third element)
		filesChanged := append(commit.Added, commit.Modified...)
		filesChanged = append(filesChanged, commit.Removed...)
		e("Processing commit #" + strconv.Itoa(k) + "  " + commit.ID)
		e(strconv.Itoa(len(filesChanged)) + " files to process")
		//loop through files changed....
		for _, filename := range filesChanged {
			processRequestFile(filename)
		} //end fileschanged for loop
		addToReceipt("-------------------------------", 3)
	} //end for each commit loop

	return nil
}

func processRequestFile(filename string) {
	var rulesResultCurrent RuleResult
	var allowedString string

	filenameWithPrefix := projrootprefix + "/" + filename
	rulesResultCurrent.path = filenameWithPrefix
	rulesResultCurrent.originalpath = filename
	rulesResultCurrent.allowed = -1 //by default we don't know if file is allowed (1) or not allowed (0)
	e("file " + filenameWithPrefix)
	//loop through rules to check if files is "under control"
	for _, rule := range rules {
		addToReceipt("Applying rule "+rule.path, 4)
		if strings.Contains(filenameWithPrefix, rule.path) {
			addToReceipt("Rule matches file", 4)
			//we have a match, add the rule to the list of rules applied to the current file...
			rulesResultCurrent.rulesApplied = append(rulesResultCurrent.rulesApplied, rule)
			rulesResultCurrent.allowed = rule.allowed
		} else { //end if rule match the path...
			addToReceipt("Rule does not match file", 4)
		}
	} //end rules for loop

	//keep some statistics....
	if rulesResultCurrent.allowed == 1 {
		rulesOK++
		allowedString = "ALLOWED"
	} else if rulesResultCurrent.allowed == 0 {
		rulesKO++
		allowedString = "NOT ALLOWED"
	} else {
		rulesNA++
		allowedString = "NOT MONITORED"
	}

	addToReceipt("File matched by "+strconv.Itoa(len(rulesResultCurrent.rulesApplied))+" rules, the final result is "+allowedString, 2)

	//add the processed file to the list of processed files...
	rulesResults = append(rulesResults, rulesResultCurrent)
	addToReceipt("-------------------------------", 4)
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
		addToReceipt("Importing line  #"+strconv.Itoa(c)+"  ["+line+"]", 6)
		if len(line) < 3 {
			addToReceipt("Line too short, considered empty. Skipped", 6)
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
			addToReceipt("Line is a comment, skipped", 6)
			break
		default:
			addToReceipt("Prefix not valid, skipped", 6)
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
	rule := new(Rule)
	if isOKK {
		rulesOK++
		rule.allowed = 1
	} else {
		rulesKO++
		rule.allowed = 0
	}
	rule.originalpath = line
	if line[0:1] == "/" {
		line = projrootprefix + line
	}
	rule.path = line
	rules = append(rules, *rule)
	addToReceipt("rule ["+rule.originalpath+"] is allowed ["+strconv.FormatBool(isOKK)+"]", 6)
	return nil
}

func loadConfigMetadata(line string) (error) {
	index := strings.Index(line, "=")
	if index < 0 {
		addToReceipt("Unable to find [=] assignment in metadata element", 6)
		return errors.New("metadata line bad syntax, missing assignment operator")
	}

	key := strings.TrimSpace(line[:index])
	value := strings.TrimSpace(line[index:])
	if len(key) == 0 {
		addToReceipt("unable to find key in metadata element", 6)
		return errors.New("metadata line bad syntax, key is empty")
	}
	if len(value) == 0 {
		addToReceipt("unable to find value in metadata element", 6)
		return errors.New("metadata line bad syntax, value is empty")
	}
	addToReceipt("element ["+key+"] added to metadata with value ["+value+"]", 6)
	metadata[key] = value

	//update known parameters...
	if key == "verboselevel" {
		valueToInt, _ := strconv.Atoi(value)
		addToReceipt("updating receipt log level to "+strconv.Itoa(valueToInt), 2)
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
