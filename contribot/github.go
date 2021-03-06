package contribot

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/go-martini/martini"
	"github.com/martini-contrib/csrf"
	"github.com/martini-contrib/render"
	"github.com/martini-contrib/sessions"
	"io/ioutil"
	"labix.org/v2/mgo"
	"log"
	"net/http"
	"net/url"
	"os"
)

const (
	gitHubAPIURL = "https://api.github.com"
	acceptHeader = "application/json"
	userAgent    = "ContriBot"
)

func handleGitHook(req *http.Request, res http.ResponseWriter, db *mgo.Database) {
	if req.Header.Get("X-GitHub-Event") != "pull_request" {
		log.Println("Unsed GitHub Payload")
		res.WriteHeader(http.StatusOK)
		return
	}
	log.Println("Received Pull Request Payload")

	err := req.ParseForm()
	if err != nil {
		log.Println(err)
	}
	rawPayload := req.PostForm.Get("payload")
	var payload map[string]interface{}
	err = json.Unmarshal([]byte(rawPayload), &payload)

	pullRequest := payload["pull_request"].(map[string]interface{})
	mergedPullRequest := pullRequest["merged"].(bool)

	if mergedPullRequest {
		userInfo := pullRequest["user"].(map[string]interface{})
		scheduled := scheduleContributor(db, userInfo["login"].(string))
		if scheduled {
			log.Printf("New Contributor: %s", userInfo["login"])
			repository := payload["repository"].(map[string]interface{})
			repoName := repository["full_name"].(string)
			pullRequestNumber := fmt.Sprintf("%.0f", pullRequest["number"].(float64))
			go postRewardInvite(repoName, pullRequestNumber)
		}
	}
	res.WriteHeader(http.StatusOK)
}

func postRewardInvite(repoName, prNumber string) {
	requestURL := gitHubAPIURL + "/repos/" + repoName + "/issues/" + prNumber + "/comments"
	payload := make(map[string]string)
	payload["body"] = "Hey! Awesome job! We wish to reward you! " +
		"Please follow the following link. It will ask you to authenticate " +
		"with your GitHub Account. After that just submit some info and you " +
		"will be rewarded! \n\n" + "[Click Here!](" + os.Getenv("DOMAIN") + "/auth)" +
		"\n\n Once again, you are AWESOME!"
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", requestURL, bytes.NewReader(body))
	req.Header.Add("Accept", acceptHeader)
	req.Header.Add("Authorization", "token "+os.Getenv("GITHUB_TOKEN"))
	_, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println(err)
	}
}

func authGitHub(req *http.Request, res http.ResponseWriter) {
	querystring := url.Values{}
	querystring.Set("client_id", os.Getenv("GITHUB_CLIENT_ID"))
	querystring.Set("redirect_uri", os.Getenv("DOMAIN")+"/githubAuth")
	querystring.Set("scope", "user")
	urlStr := "https://github.com/login/oauth/authorize?" + querystring.Encode()
	http.Redirect(res, req, urlStr, http.StatusFound)
}

func gitHubAuthMiddleware(req *http.Request, res http.ResponseWriter, r render.Render, c martini.Context) {
	// Verify origin is GH
	template := make(map[string]string)
	template["contactUrl"] = os.Getenv("CONTACT_URL")
	template["contactValue"] = os.Getenv("CONTACT_VALUE")
	template["message"] = "There was an authenticating your account."
	err := req.ParseForm()
	if err != nil {
		log.Println(err)
		r.HTML(http.StatusBadRequest, "error", template)
		return
	}
	if len(req.Form["code"]) != 1 {
		r.HTML(http.StatusUnauthorized, "error", template)
		return
	}
	// If legit, attempt to get token
	payload := make(map[string]string)
	payload["client_id"] = os.Getenv("GITHUB_CLIENT_ID")
	payload["client_secret"] = os.Getenv("GITHUB_CLIENT_SECRET")
	payload["code"] = req.Form["code"][0]
	body, _ := json.Marshal(payload)
	ghReq, _ := http.NewRequest("POST", "https://github.com/login/oauth/access_token", bytes.NewReader(body))
	ghReq.Header.Add("Content-Type", acceptHeader)
	ghReq.Header.Add("Accept", acceptHeader)
	ghReq.Header.Add("User-Agent", userAgent)
	ghRes, err := http.DefaultClient.Do(ghReq)

	// check status code
	if err != nil {
		log.Println(err)
		r.HTML(http.StatusServiceUnavailable, "error", template)
		return
	}
	ghPayload, err := ioutil.ReadAll(ghRes.Body)
	if err != nil {
		log.Println(err)
		r.HTML(http.StatusInternalServerError, "error", template)
		return
	}
	var ghJSON map[string]interface{}
	err = json.Unmarshal(ghPayload, &ghJSON)
	if err != nil {
		log.Println(err)
		r.HTML(http.StatusInternalServerError, "error", template)
		return
	}
	token, ok := ghJSON["access_token"].(string)
	if !ok {
		r.HTML(http.StatusOK, "error", template)
		return
	}
	c.Map(token)
	c.Next()
	http.Redirect(res, req, "/award", http.StatusFound)
}

func getUserFromToken(r render.Render, token string, session sessions.Session) {
	template := make(map[string]string)
	template["contactUrl"] = os.Getenv("CONTACT_URL")
	template["contactValue"] = os.Getenv("CONTACT_VALUE")
	template["message"] = "GitHub seems to have troubles :/"

	qs := url.Values{}
	qs.Set("access_token", token)
	ghReq, _ := http.NewRequest("GET", gitHubAPIURL+"/user?"+qs.Encode(), nil)
	ghReq.Header.Add("User-Agent", userAgent)
	ghRes, err := http.DefaultClient.Do(ghReq)
	if err != nil {
		log.Println(err)
		r.HTML(http.StatusServiceUnavailable, "error", template)
		return
	}
	ghPayload, err := ioutil.ReadAll(ghRes.Body)
	if err != nil {
		log.Println(err)
		r.HTML(http.StatusInternalServerError, "error", template)
		return
	}
	ghRes.Body.Close()
	var ghJSON map[string]interface{}
	err = json.Unmarshal(ghPayload, &ghJSON)
	if err != nil {
		log.Println(err)
		r.HTML(http.StatusInternalServerError, "error", template)
		return
	}

	user, ok := ghJSON["login"].(string)
	if !ok {
		log.Println("Obtaining username from request failed.")
		r.HTML(http.StatusInternalServerError, "error", template)
	}
	session.Set("user", user)
}

func awardUser(db *mgo.Database, session sessions.Session, r render.Render, x csrf.CSRF) {
	template := make(map[string]string)
	template["contactUrl"] = os.Getenv("CONTACT_URL")
	template["contactValue"] = os.Getenv("CONTACT_VALUE")
	user := session.Get("user").(string)
	status := checkStatus(db, user)
	if status == 0 {
		template["message"] = "Can't seem to find records of you :/"
		r.HTML(http.StatusUnauthorized, "error", template)
	} else if status == 1 {
		err := userHasAuth(db, user)
		if err != nil {
			log.Println(err)
			template["message"] = "Uh oh! Please report this :("
			r.HTML(http.StatusInternalServerError, "error", template)
		} else {
			r.HTML(http.StatusOK, "form", x.GetToken())
		}
	} else if status == 2 {
		r.HTML(http.StatusOK, "form", x.GetToken())
	} else if status == 3 {
		template["message"] = "Hey buddy, it seems you have been awarded before."
		r.HTML(http.StatusUnauthorized, "error", template)
	}
}

func handleSubmission(req *http.Request, r render.Render, db *mgo.Database, session sessions.Session, backends []Backend) {
	template := make(map[string]string)
	template["contactUrl"] = os.Getenv("CONTACT_URL")
	template["contactValue"] = os.Getenv("CONTACT_VALUE")
	template["message"] = "Something went wrong :'("
	err := req.ParseForm()
	if err != nil {
		r.HTML(http.StatusBadRequest, "error", template)
	}
	user := session.Get("user").(string)
	err = userHasSubmitted(db, user)

	if err != nil {
		log.Println(err)
		r.HTML(http.StatusInternalServerError, "error", template)
	} else {
		submission := &Submission{
			Name:    req.PostForm.Get("name"),
			Address: req.PostForm.Get("address"),
			Email:   req.PostForm.Get("email"),
			Size:    req.PostForm.Get("size"),
		}
		for i := 0; i < len(backends); i++ {
			go backends[i](submission)
		}
		r.HTML(http.StatusOK, "success", nil)
	}
}
