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
	"github.com/joho/godotenv"
	"github.com/bluele/slack"
)

var isLAMBDA bool
var isDOCKER bool
var isPROD bool
var metadata map[string]string //we could add {} at the end to initialise the map...
var rules []Rule
var rulesOK int
var rulesKO int
var rulesResults []RuleResult
var rulesResultsCountKO int
var projrootprefix = "[PROOT]"
var configFileName = "config"
var dummyPayloadFileName = "payload"
var verboseReceipt int
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
	Pusher struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	} `json:"pusher"`
	Repository struct {
		FullName string `json:"full_name"`
	}
	Ref string `json:"ref"`
}

func main() {
	checkIfLambda()
	if isLAMBDA {
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
	//no need to waste time/resources if no commits....
	if len(request.Commits) == 0 {
		return "NO COMMITS FOUND!", nil
	}
	
	//initialise
	metadata = make(map[string]string)
	rulesOK = 0
	rulesKO = 0
	rulesResultsCountKO = 0
	verboseReceipt = 0
	receipt = []Receipt{}
	rulesResults = []RuleResult{}
	rules = []Rule{}

	greetings()

	//read .env variables
	if err := godotenv.Load(); err != nil {
		return "unable to read .env file", err
	}
	if err := loadConfig(); err != nil {
		return "", err
	}
	processRequest(request)
	sendReceipt(request)
	return "Process completed", nil
}

func processRequest(request Request) (error) {
	addToReceipt(strconv.Itoa(len(request.Commits))+" commits found in the payload", true)

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
		allowedString = "ALLOWED"
	} else if rulesResultCurrent.allowed == 0 {
		rulesResultsCountKO++
		allowedString = "NOT ALLOWED"
	} else {
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
	value := strings.TrimSpace(line[index+1:])
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

func sendReceipt(request Request) {
	var message string
	var emoji string
	emoji = os.Getenv("SLACK_EMOJI_OK")

	message += "RECEIPT GENERATED " + getDT() + "\n\n"
	message += "*" + strconv.Itoa(rulesResultsCountKO) + " FILES MATCHED PROTECTED FOLDERS*\n\n"
	message += "_Repository " + request.Repository.FullName + "   (" + request.Ref + ")_\n"

	if rulesResultsCountKO > 0 {
		emoji = os.Getenv("SLACK_EMOJI_KO")
		for _, file := range rulesResults {
			if file.allowed == 0 {
				message += file.originalpath + "\n"
			} //end if rule match is not allowed
		} //end for each ruleResults
	} // end if rulesKO >0

	message += "\nPusher: " + request.Pusher.Name + "   " + request.Pusher.Email + "\n"
	message += "_isLAMBDA " + strconv.FormatBool(isLAMBDA) +
		"/isDOCKER " + strconv.FormatBool(isDOCKER) +
		"/isPROD " + strconv.FormatBool(isPROD) +
		"/fn " + os.Getenv("AWS_LAMBDA_FUNCTION_NAME") +
		"/v " + os.Getenv("AWS_LAMBDA_FUNCTION_VERSION") + "_\n"

	sendSlack(message, emoji)
}

func sendSlack(message string, emoji string) {
	var channel string
	hook := slack.NewWebHook(os.Getenv("SLACK_WEBHOOK_URL"))
	if isPROD {
		channel = os.Getenv("SLACK_CHANNEL_PROD")
	} else {
		channel = os.Getenv("SLACK_CHANNEL_DEV")
	}

	//generate a unique number to be sure slack always shows the sender...
	//this is really NOT needed
	uniqueSenderToken := " [" + strconv.Itoa(time.Now().Nanosecond()/1000000) + "]"

	err := hook.PostMessage(&slack.WebHookPostPayload{
		Text:      message,
		Channel:   channel,
		IconEmoji: emoji,
		Username:  os.Getenv("SLACK_USERNAME") + uniqueSenderToken,
	})
	if err != nil {
		panic(err)
	}
}

func greetings() {
	if isLAMBDA {
		addToReceipt("Hello Jeff", true)
	} else {
		addToReceipt("Greetings Professor Falken", true)
	}
}

func checkIfLambda() {
	//default value
	isLAMBDA = false
	isDOCKER = false
	isPROD = false
	if len(os.Getenv("AWS_REGION")) != 0 {
		isLAMBDA = true
	}
	//even if we are in an AWS/LAMBDA environment, it could be a docker container...
	//so let's use another env var to understand if docker
	if os.Getenv("AWS_ACCESS_KEY") == "SOME_ACCESS_KEY_ID" {
		isDOCKER = true
	}
	//if LAMBDA and not docker.... this is PROD!!!!
	isPROD = isLAMBDA && !isDOCKER

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
	e("ENVIRONMENT VARIABLES:")
	for _, pair := range os.Environ() {
		e(pair)
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
