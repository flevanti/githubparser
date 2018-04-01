// main.go
package main

import (
	"fmt"
	"github.com/aws/aws-lambda-go/lambda"
	"encoding/json"
	"os"
	"bufio"
	"time"
	"strconv"
	"strings"
	"errors"
	"io/ioutil"
	"log"
	"github.com/bluele/slack"
	"github.com/joho/godotenv"
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
var verboseReceipt = 0
var receipt []Receipt

type Rule struct {
	allowed      int
	path         string
	originalpath string
}
type Receipt struct {
	verboseReceipt bool
	message        string
	dateTime       string
	unixTime       int32
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

func sendSlack(message string) {
	hook := slack.NewWebHook(os.Getenv("SLACK_WEBHOOK_URL"))
	err := hook.PostMessage(&slack.WebHookPostPayload{
		Text:      message,
		Channel:   os.Getenv("SLACK_CHANNEL"),
		IconEmoji: os.Getenv("SLACK_EMOJI"),
		Username:  os.Getenv("SLACK_USERNAME"),
	})
	if err != nil {
		panic(err)
	}
}

func Handler(request Request) (string, error) {
	greetings()
	//initialise
	metadata = make(map[string]string)
	//read .env variables
	if err := godotenv.Load(); err != nil {
		return "unable to read .env file", err
	}
	if err := loadConfig(); err != nil {
		return "", err
	}
	processRequest(request)
	sendReceipt()
	return "", nil
}

func sendReceipt() {
	var message string
	message += "RECEIPT GENERATED " + getDT() + "\n\n"

	if rulesKO > 0 {
		message += "*" + strconv.Itoa(rulesKO) + " files matched protected paths*\n\n"
	}

	sendSlack(message)
}

func processRequest(request Request) (error) {
	if len(request.Commits) > 0 {
		addToReceipt(strconv.Itoa(len(request.Commits))+" commits found in the payload", true)
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
		addToReceipt("Processing commit #"+strconv.Itoa(k)+"  "+commit.ID, true)
		addToReceipt(strconv.Itoa(len(filesChanged))+" files to process", true)
		//loop through files changed....
		for _, filename := range filesChanged {
			processRequestFile(filename)
		} //end fileschanged for loop
		addToReceipt("-------------------------------", true)
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
	addToReceipt("file "+filenameWithPrefix, true)
	//loop through rules to check if files is "under control"
	for _, rule := range rules {
		addToReceipt("Applying rule "+rule.path, true)
		if strings.Contains(filenameWithPrefix, rule.path) {
			addToReceipt("Rule matches file", true)
			//we have a match, add the rule to the list of rules applied to the current file...
			rulesResultCurrent.rulesApplied = append(rulesResultCurrent.rulesApplied, rule)
			rulesResultCurrent.allowed = rule.allowed
		} else { //end if rule match the path...
			addToReceipt("Rule does not match file", true)
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

	addToReceipt("File matched by "+strconv.Itoa(len(rulesResultCurrent.rulesApplied))+" rules, the final result is "+allowedString, true)

	//add the processed file to the list of processed files...
	rulesResults = append(rulesResults, rulesResultCurrent)
	addToReceipt("-------------------------------", true)
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

func fileExists(file string) (bool) {
	if _, err := os.Stat(file); err == nil {
		return true
	}

	return false
}

func loadConfig() (error) {
	addToReceipt("Reading config file ["+configFileName+"]", false)
	var line string
	var c int
	var prefix string
	var err error
	if !fileExists(configFileName) {
		addToReceipt("Reading config file ["+configFileName+"]", false)
		return errors.New("config file not found")
	}
	fileHandle, _ := os.Open(configFileName)
	defer fileHandle.Close()
	fileScanner := bufio.NewScanner(fileHandle)
	for fileScanner.Scan() {
		c++
		line = fileScanner.Text()
		addToReceipt("Importing line  #"+strconv.Itoa(c)+"  ["+line+"]", true)
		if len(line) < 3 {
			addToReceipt("Line too short, considered empty. Skipped", true)
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
			addToReceipt("Line is a comment, skipped", true)
			break
		default:
			addToReceipt("Prefix not valid, skipped", true)
		} //end switch
		if err != nil {
			return err
		}
	} //end Scan loop

	addToReceipt("Configuration file loaded: "+
		strconv.Itoa(rulesOK)+ " OK rules, "+
		strconv.Itoa(rulesKO)+ " KO rules, "+
		strconv.Itoa(len(metadata))+ " metadata", true)
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
	addToReceipt("rule ["+rule.originalpath+"] is allowed ["+strconv.FormatBool(isOKK)+"]", true)
	return nil
}

func loadConfigMetadata(line string) (error) {
	index := strings.Index(line, "=")
	if index < 0 {
		addToReceipt("Unable to find [=] assignment in metadata element", true)
		return errors.New("metadata line bad syntax, missing assignment operator")
	}

	key := strings.TrimSpace(line[:index])
	value := strings.TrimSpace(line[index:])
	if len(key) == 0 {
		addToReceipt("unable to find key in metadata element", true)
		return errors.New("metadata line bad syntax, key is empty")
	}
	if len(value) == 0 {
		addToReceipt("unable to find value in metadata element", true)
		return errors.New("metadata line bad syntax, value is empty")
	}
	addToReceipt("element ["+key+"] added to metadata with value ["+value+"]", true)
	metadata[key] = value

	//update known parameters...
	if key == "verbosereceipt" {
		valueToInt, _ := strconv.Atoi(value)
		addToReceipt("updating receipt log level to "+strconv.Itoa(valueToInt), true)
		verboseReceipt = valueToInt
	}

	return nil
}

func greetings() {
	if isAWS {
		addToReceipt("Hello Jeff", true)
	} else {
		addToReceipt("Greetings Professor Falken", true)
	}
}

func checkIfAWS() {
	if len(os.Getenv("AWS_REGION")) != 0 {
		isAWS = true
	} else {
		isAWS = false
	}
}

func addToReceipt(line string, verboseReceipt bool) {

	receiptRecord := new(Receipt)
	receiptRecord.verboseReceipt = verboseReceipt
	receiptRecord.message = line
	receiptRecord.dateTime = getDT()
	receiptRecord.unixTime = int32(time.Now().Unix())
	receipt = append(receipt, *receiptRecord)
	e(line + "  [RECEIPT]")
}

func e(line string) {
	fmt.Println(getDT() + "  " + line)
}

func getDT() (string) {
	// time date formatting...
	// https://golang.org/src/time/format.go
	return time.Now().Format("2006-01-02 15:04:05.0000")
}

func printEnvVars() {
	for _, pair := range os.Environ() {
		fmt.Println(pair)
	}
}

func loadDummyPayloadFile() (Request) {
	if fileExists(dummyPayloadFileName) {
		var request Request

		content, err := ioutil.ReadFile(dummyPayloadFileName)
		if err != nil {
			log.Fatal(err)
		}
		_ = json.Unmarshal(content, &request)
		addToReceipt("Dummy payload loaded", true)
		return request
	}
	panic("DUMMY PAYLOAD FILE NOT FOUND")
}
