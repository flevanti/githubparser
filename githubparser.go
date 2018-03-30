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
)

var isAWS bool
var metadata map[string]string //we could add {} at the end to initialise the map...
var rules []string

// this is the structure of the github webhook payload
// element not needed are commented to use less memory
// structure obtained thanks to
//
// http://json2struct.mervine.net/
type Request struct {
	//	After   string      `json:"after"`
	//	BaseRef interface{} `json:"base_ref"`
	//	Before  string      `json:"before"`
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
	//	Compare string `json:"compare"`
	//	Created bool   `json:"created"`
	//	Deleted bool   `json:"deleted"`
	//	Forced  bool   `json:"forced"`
	//	HeadCommit struct {
	//		Added []string `json:"added"`
	//		Author struct {
	//			Email    string `json:"email"`
	//			Name     string `json:"name"`
	//			Username string `json:"username"`
	//		} `json:"author"`
	//		Committer struct {
	//			Email    string `json:"email"`
	//			Name     string `json:"name"`
	//			Username string `json:"username"`
	//		} `json:"committer"`
	//		Distinct  bool          `json:"distinct"`
	//		ID        string        `json:"id"`
	//		Message   string        `json:"message"`
	//		Modified  []interface{} `json:"modified"`
	//		Removed   []interface{} `json:"removed"`
	//		Timestamp string        `json:"timestamp"`
	//		TreeID    string        `json:"tree_id"`
	//		URL       string        `json:"url"`
	//	} `json:"head_commit"`
	//	Pusher struct {
	//		Email string `json:"email"`
	//		Name  string `json:"name"`
	//	} `json:"pusher"`
	//	Ref string `json:"ref"`
	//	Repository struct {
	//		ArchiveURL       string      `json:"archive_url"`
	//		Archived         bool        `json:"archived"`
	//		AssigneesURL     string      `json:"assignees_url"`
	//		BlobsURL         string      `json:"blobs_url"`
	//		BranchesURL      string      `json:"branches_url"`
	//		CloneURL         string      `json:"clone_url"`
	//		CollaboratorsURL string      `json:"collaborators_url"`
	//		CommentsURL      string      `json:"comments_url"`
	//		CommitsURL       string      `json:"commits_url"`
	//		CompareURL       string      `json:"compare_url"`
	//		ContentsURL      string      `json:"contents_url"`
	//		ContributorsURL  string      `json:"contributors_url"`
	//		CreatedAt        int         `json:"created_at"`
	//		DefaultBranch    string      `json:"default_branch"`
	//		DeploymentsURL   string      `json:"deployments_url"`
	//		Description      string      `json:"description"`
	//		DownloadsURL     string      `json:"downloads_url"`
	//		EventsURL        string      `json:"events_url"`
	//		Fork             bool        `json:"fork"`
	//		Forks            int         `json:"forks"`
	//		ForksCount       int         `json:"forks_count"`
	//		ForksURL         string      `json:"forks_url"`
	//		FullName         string      `json:"full_name"`
	//		GitCommitsURL    string      `json:"git_commits_url"`
	//		GitRefsURL       string      `json:"git_refs_url"`
	//		GitTagsURL       string      `json:"git_tags_url"`
	//		GitURL           string      `json:"git_url"`
	//		HasDownloads     bool        `json:"has_downloads"`
	//		HasIssues        bool        `json:"has_issues"`
	//		HasPages         bool        `json:"has_pages"`
	//		HasProjects      bool        `json:"has_projects"`
	//		HasWiki          bool        `json:"has_wiki"`
	//		Homepage         string      `json:"homepage"`
	//		HooksURL         string      `json:"hooks_url"`
	//		HTMLURL          string      `json:"html_url"`
	//		ID               int         `json:"id"`
	//		IssueCommentURL  string      `json:"issue_comment_url"`
	//		IssueEventsURL   string      `json:"issue_events_url"`
	//		IssuesURL        string      `json:"issues_url"`
	//		KeysURL          string      `json:"keys_url"`
	//		LabelsURL        string      `json:"labels_url"`
	//		Language         string      `json:"language"`
	//		LanguagesURL     string      `json:"languages_url"`
	//		License          interface{} `json:"license"`
	//		MasterBranch     string      `json:"master_branch"`
	//		MergesURL        string      `json:"merges_url"`
	//		MilestonesURL    string      `json:"milestones_url"`
	//		MirrorURL        interface{} `json:"mirror_url"`
	//		Name             string      `json:"name"`
	//		NotificationsURL string      `json:"notifications_url"`
	//		OpenIssues       int         `json:"open_issues"`
	//		OpenIssuesCount  int         `json:"open_issues_count"`
	//		Owner struct {
	//			AvatarURL         string `json:"avatar_url"`
	//			Email             string `json:"email"`
	//			EventsURL         string `json:"events_url"`
	//			FollowersURL      string `json:"followers_url"`
	//			FollowingURL      string `json:"following_url"`
	//			GistsURL          string `json:"gists_url"`
	//			GravatarID        string `json:"gravatar_id"`
	//			HTMLURL           string `json:"html_url"`
	//			ID                int    `json:"id"`
	//			Login             string `json:"login"`
	//			Name              string `json:"name"`
	//			OrganizationsURL  string `json:"organizations_url"`
	//			ReceivedEventsURL string `json:"received_events_url"`
	//			ReposURL          string `json:"repos_url"`
	//			SiteAdmin         bool   `json:"site_admin"`
	//			StarredURL        string `json:"starred_url"`
	//			SubscriptionsURL  string `json:"subscriptions_url"`
	//			Type              string `json:"type"`
	//			URL               string `json:"url"`
	//		} `json:"owner"`
	//		Private         bool   `json:"private"`
	//		PullsURL        string `json:"pulls_url"`
	//		PushedAt        int    `json:"pushed_at"`
	//		ReleasesURL     string `json:"releases_url"`
	//		Size            int    `json:"size"`
	//		SSHURL          string `json:"ssh_url"`
	//		Stargazers      int    `json:"stargazers"`
	//		StargazersCount int    `json:"stargazers_count"`
	//		StargazersURL   string `json:"stargazers_url"`
	//		StatusesURL     string `json:"statuses_url"`
	//		SubscribersURL  string `json:"subscribers_url"`
	//		SubscriptionURL string `json:"subscription_url"`
	//		SvnURL          string `json:"svn_url"`
	//		TagsURL         string `json:"tags_url"`
	//		TeamsURL        string `json:"teams_url"`
	//		TreesURL        string `json:"trees_url"`
	//		UpdatedAt       string `json:"updated_at"`
	//		URL             string `json:"url"`
	//		Watchers        int    `json:"watchers"`
	//		WatchersCount   int    `json:"watchers_count"`
	//	} `json:"repository"`
	//	Sender struct {
	//		AvatarURL         string `json:"avatar_url"`
	//		EventsURL         string `json:"events_url"`
	//		FollowersURL      string `json:"followers_url"`
	//		FollowingURL      string `json:"following_url"`
	//		GistsURL          string `json:"gists_url"`
	//		GravatarID        string `json:"gravatar_id"`
	//		HTMLURL           string `json:"html_url"`
	//		ID                int    `json:"id"`
	//		Login             string `json:"login"`
	//		OrganizationsURL  string `json:"organizations_url"`
	//		ReceivedEventsURL string `json:"received_events_url"`
	//		ReposURL          string `json:"repos_url"`
	//		SiteAdmin         bool   `json:"site_admin"`
	//		StarredURL        string `json:"starred_url"`
	//		SubscriptionsURL  string `json:"subscriptions_url"`
	//		Type              string `json:"type"`
	//		URL               string `json:"url"`
	//	} `json:"sender"`
}

func main() {
	//initialise
	metadata = make(map[string]string)
	checkIfAWS()
	loadConfig()
	if isAWS {
		lambda.Start(Handler)
	} else {
		request := loadDummyPayloadFile()
		response, err := Handler(request)
		if err == nil {
			fmt.Print(response + "\n")
		}

	}
}

func loadDummyPayloadFile() (Request) {
	file := "payload"
	if (fileExists(file)) {
		var request Request

		content, err := ioutil.ReadFile(file)
		if err != nil {
			log.Fatal(err)
		}
		err = json.Unmarshal(content, &request)
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

func loadConfig() {
	file := "config"
	if fileExists(file) {
		fileHandle, _ := os.Open(file)
		defer fileHandle.Close()
		fileScanner := bufio.NewScanner(fileHandle)

		for fileScanner.Scan() {
			fmt.Println(fileScanner.Text())
		}
	} else {
		panic("CONFIG NOT FOUND")
	}

}

func greetings() {
	if isAWS {
		fmt.Println("Hello Jeff")
	} else {
		fmt.Println("Greetings Professor Falken")
	}
}

func Handler(request Request) (string, error) {
	greetings()
	//fmt.Printf("Hello %+v \n", len(request.Commits))
	return "", nil
}

func checkIfAWS() {
	if len(os.Getenv("AWS_REGION")) != 0 {
		isAWS = true
	} else {
		isAWS = false
	}
}

func printEnvVars() {
	for _, pair := range os.Environ() {
		fmt.Println(pair)
	}
}

/*

GOOS=linux go build githubparser && \
rm -f githubparser.zip && \
zip githubparser.zip githubparser && \
docker run --rm -v "$PWD":/var/task lambci/lambda:go1.x githubparser '{"ID": "fd"}'
*/
