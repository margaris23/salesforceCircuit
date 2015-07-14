/**
  Prototype: Salesforce extension notification service
  Info: Currently only 1 user (bot-user in the future
        can post new Leads from a specific salesforce
		account. The webhook creator service used in this
		prototype uses OAuth in order to trust only one
		salesforce user for being notified for Lead changes.
**/
// TODO make LEADS persistent
// TODO Support more users
package main

import (
	"fmt"
	"net/http"
	"log"
	"io/ioutil"
	"os"
	"crypto/tls"
	"encoding/base64"
	"time"
	"net/url"
	"encoding/json"
	"bytes"
)

// Constants
const PORT string = ":9000"
const API_URL string = "https://<CIRCUIT_BACKEND_SERVER_IP>:<PORT>/v2/"
/** 
   In order to use echo service please subscribe to 
   https://salesforce-webhook-creator.herokuapp.com/
   And use the echo service as webhook.
**/
const ECHO_SERVICE string = "http://echo-webhook.herokuapp.com/lead"
const POLLING_PERIOD int = 5
const CONVS_URL string = API_URL + "conversations"
// This is the credentials of the Ansible User
const CREDENTIALS string = "<USER_NAME>:<USER_PASSWORD>"
const PROXY_URL string = "<PROXY_IP>:<PROXY_PORT>"
const ITEMS_URL string = "items"

// Vars
// If no conversation is configured then no posts will occur
var convId string = ""

// HTTP-request handler
func send(method string, url string, client *http.Client, useAuth bool, content []byte) string {
	// encode credentials
	encoded := base64.StdEncoding.EncodeToString([]byte(CREDENTIALS))
	
	// create content for POST
	var buf bytes.Buffer
	if method == "POST" {
		buf = *bytes.NewBuffer(content)
	}

	// Create request
	req, _ := http.NewRequest(method, url, &buf)

	// Add necessary headers
	req.Header.Add("Content-Type", "application/json")
	if useAuth {
		// Only basic authentication is currently supported
		req.Header.Add("Authorization", "Basic " + encoded)
	}
	
	// SEND request
	res, err := client.Do(req)
	if nil != err {
		log.Fatal("Cannot send" + err.Error())
		os.Exit(1)
	}
	
	// Handle reply
	defer res.Body.Close()
	body, err2 := ioutil.ReadAll(res.Body)
	if err2 != nil {
		fmt.Println("ERROR: " + err2.Error())
		return ""
	}
	
	return string(body)
}

// Lead Model
type Lead struct {
	Id string	`json:Id`
	Description string	`json:Description`
	URL string
	Created string `json:CreatedDate`
	Company string `json:Company`
}
// String adapter for Lead
func (l Lead) String() string {
	return fmt.Sprintf("<b>Company</b>: %v<br><b>CreatedAt</b>: %v<br><br><b>Description</b>: %v", l.Company, l.Created, l.Description)
}

// Global Map: id => Lead
var LEADMAP = make(map[string]Lead)

// server's configuration page
func configureHandler (w http.ResponseWriter, req *http.Request){
	if req.Method == "GET" {
		log.Println("Configure - Request")
		http.ServeFile(w, req, "conf.html")
		return
	}
	// Handle POST here
	convId = req.FormValue("convId")
	log.Println("Configured Conversation ID: " + convId)
	w.WriteHeader(http.StatusOK)
}

// Extract leads from json reply
func parseLeads (reply string) []Lead {
	// create empty object
	var f interface{}
	
	// unmarshall it
	err := json.Unmarshal([]byte(reply), &f)
	if err != nil {
		log.Fatalln("Error parsing: " + err.Error())
		os.Exit(2)
	}
	
	// maximum 10 per 5sec (for demo)
	leads := make([]Lead, 0, 10)

	// create type assertion map
	m := f.(map[string]interface{})
	for k, v := range m {
		// type assertion for array object of name "new"
		if vv, found := v.([]interface{}); found && k == "new" {
			// parse 'new' array i.e leads
			for _, u := range vv {
				lm := u.(map[string]interface{})
				// parse lead properties
				lead := &Lead{}
				for key, val := range lm {
					switch key {
						case "Id":
							lead.Id = val.(string)
						case "Description":
							lead.Description = val.(string)
						case "attributes":
							attrs := val.(map[string]interface{})
							for ka, kv := range attrs {
								if ka == "url" {
									lead.URL = kv.(string)
								}
							}
						case "Company":
							lead.Company = val.(string)
						case "CreatedDate":
							lead.Created = val.(string)
						// TODO add support for more fields here
					}
				}
				leads = append(leads, *lead)
			}
		}
	}
	return leads
}

// periodically check for leads (polling due to infr. restrictions)
func poll(salesforce_client *http.Client, circuit_client *http.Client) {
	log.Println("polling...")
	for {
		// IfconversationId is not configured
		if convId == "" {
			log.Println("No conversation Id configured yet!")
			// Wait for 5sec
			time.Sleep(5 * time.Second)
			continue
		}
		// Get leads from Echo-WebHook-Service
		reply := send("GET", ECHO_SERVICE, salesforce_client, false, nil)
		
		// Extract leads from reply
		leads := parseLeads(reply)

		// Find non-existing leads and create new posts to circuit
		for _, lead := range leads {
			if _, ok := LEADMAP[lead.Id]; !ok {
				log.Println("New lead found ==> ", lead.Id)
				// create new posts
				content := []byte(`{"content":"` + lead.String() + `", "subject": "New Lead!!!"}`)
				send("POST", CONVS_URL + "/" + convId + "/messages", circuit_client, true, content)
				// update status (map)
				LEADMAP[lead.Id] = lead
			}
		}
		
		// Wait for 5sec
		time.Sleep(5 * time.Second)
	}
}

func main() {
	// CIRCUIT Client
	proxyurl, _ := url.Parse(PROXY_URL)
	circuit_tr := &http.Transport{
		// Ignore SSL errors
        TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
    }
    circuit_client := &http.Client{Transport: circuit_tr}
	
	// SALESFORCE Client
	salesforce_tr := &http.Transport{
        TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		// Configure proxy
		Proxy: http.ProxyURL(proxyurl),
    }
	salesforce_client := &http.Client{Transport: salesforce_tr}
	
	// SERVER (Extension App)
	mux := http.NewServeMux()
	
	// Server Routes
	mux.HandleFunc("/configure", configureHandler)

	// periodically check for leads (polling due to infr. restrictions)
	go poll(salesforce_client, circuit_client)

	// Start server!
	fmt.Println("... listening on " + PORT)
	if err := http.ListenAndServe(PORT, mux); nil != err {
		log.Fatal(err.Error())
	}
}
