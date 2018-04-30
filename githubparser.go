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
	"sync"
)

var isLAMBDA bool //running using lambda function (dev or aws)
var isDOCKER bool //running in docker (so this is probably a lambda function)
var isAWS bool    //running in AWS

var metadata map[string]string //we could add {} at the end to initialise the map...
var rules []Rule
var rulesOK int
var rulesKO int
var rulesResults []RuleResult
var rulesResultsCountKO int
var projrootprefix = "[PROOT]"
var configFileName = "config"
var dummyPayloadFileName = "payload.json"
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

/**
There're mainly 3 ways this script can be run....
- running the go app directly on local machine
- running it using a local lambda function (thanks to docker & lambci/lambda )
- running on AWS as a lambda function

based on the way we are running it the request payload can be obtained in different ways

 */

func main() {
	checkEnvContext()
	if isLAMBDA {
		lambda.Start(Handler)
	} else {
		response, err := Handler(Request{})
		e(response)
		if err != nil {
			e(err.Error())
		}
	}
}

func Handler(request Request) (string, error) {
	e(getLocalEnvSituationString())
	printEnvVars()
	if !isAWS {
		request = LoadDummyPayload()
	}

	//no need to waste time/resources if no commits....
	if len(request.Commits) == 0 {
		return "NO COMMITS FOUND!", nil
	} else {

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

	}
	addToReceipt(getLocalEnvSituationString(), true)
	return "Process completed", nil
}

func getLocalEnvSituationString() string {
	return fmt.Sprintf("isLAMBDA [%t]  isDOCKER [%t]  isAWS [%t]", isLAMBDA, isDOCKER, isAWS)

}

func processRequest(request Request) (error) {
	addToReceipt(strconv.Itoa(len(request.Commits))+" commits found in the payload", true)

	// implementing concurrency with a _primitive_ waitgroup.
	// we tested the script with a commit with 6000 files changed and
	// the impact on memory was still acceptable (75~95MB)
	// compared to time consumed.
	// AWS lambda min memory size is 128MB so makes sense to try to use it as much as possible and try to lower the time.
	// If this method fails, we will implement a buffered channel
	var wg sync.WaitGroup

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
			wg.Add(1)
			processRequestFile(filename, &wg)
		} //end fileschanged for loop
		addToReceipt("-------------------------------", true)
	} //end for each commit loop
	e("Waiting for all requests to be completed...")
	timeWaitedStart := time.Now()
	wg.Wait()
	timeWaited := time.Since(timeWaitedStart)
	e(fmt.Sprintf("Time spent waiting %.2f ms", float32(timeWaited.Nanoseconds())/1000))
	return nil
}

func processRequestFile(filename string, wg *sync.WaitGroup) {

	defer wg.Done()

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

func LoadDummyPayload() (Request) {
	var content string
	var request Request
	// look for payload in an env variable....
	content = os.Getenv("AWS_LAMBDA_EVENT_BODY")
	if content == "" {
		//if payload is not found try to use a file content
		content = loadDummyPayloadFile()
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
		case "MDT", "MTD": // metadata
			err = loadConfigMetadata(line)
		case "OKK", "KOO": // actual rule
			err = loadConfigRule(line, prefix == "OKK")
			break
		case "###", "///", "---": // comments
			addToReceipt("Line is a comment, skipped", true)
			break
		default: // unknown
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
	rule := Rule{}
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
	rules = append(rules, rule)
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
	var filesList string

	if verboseReceipt == 1 {
		message += "VERBOSE "
	}
	message += "RECEIPT GENERATED " + getDT() + "\n\n"
	message += "*" + strconv.Itoa(rulesResultsCountKO) + " FILES MATCHED PROTECTED FILES/FOLDERS*\n\n"
	message += "_Repository " + request.Repository.FullName + "   (" + request.Ref + ")_\n"

	for _, file := range rulesResults {
		if verboseReceipt == 0 {
			if file.allowed == 0 {
				filesList += file.originalpath + "\n"
			}
		} else {
			if file.allowed == 0 {
				filesList += "* "
			} else {
				filesList += "  "
			}
			filesList += file.originalpath + "\n"

		}
	} //end for each ruleResults

	message += "\nPusher: " + request.Pusher.Name + "   " + request.Pusher.Email + "\n"
	message += "_" + getLocalEnvSituationString() + "_\n"

	sendSlack(message, filesList, rulesResultsCountKO == 0)
}

func sendSlack(message string, filesList string, okFlag bool) {
	var channel string
	var emoji string
	var attachmentTitle string
	var attachmentColor string
	if filesList != "" {
		attachmentTitle = "FILES LIST"
	}

	if okFlag {
		emoji = os.Getenv("SLACK_EMOJI_OK")
	} else {
		emoji = os.Getenv("SLACK_EMOJI_KO")
		attachmentColor = "#FF0000"
	}

	hook := slack.NewWebHook(os.Getenv("SLACK_WEBHOOK_URL"))
	if isAWS {
		channel = os.Getenv("SLACK_CHANNEL_LIVE")
	} else {
		channel = os.Getenv("SLACK_CHANNEL_DEV")
	}

	//generate a unique number to be sure slack always shows the sender...
	//this is really NOT needed
	uniqueSenderToken := " [" + strconv.Itoa(time.Now().Nanosecond()/1000000) + "]"
	//create the attachment
	//slack attachments have a characters limit but I think it is more than sufficient to list errors if any.
	//for the moment this is ok, we may want to change this in the future but for the moment is ok
	attachment := []*slack.Attachment{
		{Text: filesList, Title: attachmentTitle, Color: attachmentColor},
	}
	err := hook.PostMessage(&slack.WebHookPostPayload{
		Text:        message,
		Channel:     channel,
		IconEmoji:   emoji,
		Username:    os.Getenv("SLACK_USERNAME") + uniqueSenderToken,
		Attachments: attachment,
	}, //end payload
	) //end post message

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

func checkEnvContext() {
	//default value
	isLAMBDA = false
	isDOCKER = false
	isAWS = false
	if len(os.Getenv("AWS_REGION")) != 0 {
		isLAMBDA = true
	}
	//even if we are in an AWS/LAMBDA environment, it could be a docker container...
	//so let's use another env var to understand if docker
	//after comparing docker lambda and AWS lambda I noticed that AWS_SESSION_TOKEN env var is (for the moment) only available in AWS
	if isLAMBDA && len(os.Getenv("AWS_SESSION_TOKEN")) == 0 {
		isDOCKER = true
	}
	//Try to understand if we are running in AWS
	isAWS = isLAMBDA && !isDOCKER

}

func addToReceipt(line string, OnlyForVerboseReceipt bool) {

	receiptRecord := Receipt{}
	receiptRecord.verboseReceipt = OnlyForVerboseReceipt
	receiptRecord.message = line
	receiptRecord.dateTime = getDT()
	receiptRecord.unixTime = int32(time.Now().Unix())
	receipt = append(receipt, receiptRecord)
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

func loadDummyPayloadFile() (string) {
	var content []byte
	var err error
	if fileExists(dummyPayloadFileName) {
		content, err = ioutil.ReadFile(dummyPayloadFileName)
		if err != nil {
			log.Fatal(err)
			return "{}"
		}
	}
	addToReceipt("Dummy payload loaded", true)
	return string(content)
}
