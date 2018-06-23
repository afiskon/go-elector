// Leader election based on the leader lease approach. Requires Consul.
package elector

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"time"
)

// Type of requests sent to the State Keeper process.
type requestType uint16

const (
	// Register a callback
	tREGCALLBACK requestType = iota

	// Get current leader
	tGETLEADER

	// Leader was changed
	tUPDATELEADER

	// Terminate the elector
	// TERMINATE // TODO implement
)

// Type of function called when leader changes.
type Callback func(oldLeaderId string, newLeaderId string)

// Representation of requests sent to the State Keeper process
type request struct {
	// Request type
	typetag requestType

	// tREGCALLBACK: a callback to register
	callback Callback

	// tGETLEADER: where to send a response
	respch chan<- string

	// tUPDATELEADER: new leader id (can be '')
	newLeaderId string
}

// Representation of the elector instance.
// This type is exported only for better docs, could have kept private.
type Instance struct {
	reqch chan<- request
}

// JSON description of the current leader.
type leaderInfo struct {
	LeaderId   string
	UpdateTime time.Time
}

// Consul KV response in the JSON format.
type consulResponse struct {
	LockIndex   uint64
	Key         string
	Flags       uint64
	Value       string
	CreateIndex uint64
	ModifyIndex uint64
}

// Elector configuration paramteres.
type electorConfig struct {
	selfId         string
	consulUrl      string
	leaderHoldTime time.Duration
}

// http.Client wrapper for adding new methods, particularly sendReq.
type httpClient struct {
	parent http.Client
}

// A bit more convenient method for sending HTTP requests
func (client *httpClient) sendReq(method, url string, reqBody []byte) (resp *http.Response, resBody []byte, err error) {
	req, err := http.NewRequest(method, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, nil, err
	}

	resp, err = client.parent.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	resBody, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	return resp, resBody, nil
}

// Determine current leader or elect a new one using the leader lease approach.
func getCurrentLeader(conf *electorConfig) (leaderId string, err error) {
	for {
		var repeat bool
		leaderId, repeat, err = getCurrentLeaderInternal(conf)
		if !repeat {
			break
		}
	}
	return
}

// For internal usage in getCurrentLeader function only.
func getCurrentLeaderInternal(conf *electorConfig) (leaderId string, repeat bool, err error) {
	client := httpClient{}
	resp, body, err := client.sendReq(http.MethodGet, conf.consulUrl, nil)
	if err != nil {
		err := fmt.Errorf("GET '%s' failed: '%s'", conf.consulUrl, err.Error())
		return "", false, err
	}

	var update bool
	var cas uint64

	switch {
	case resp.StatusCode == 404:
		// there is no leader yet
		update = true
	case resp.StatusCode != 200:
		err := fmt.Errorf("Unexpected HTTP status code (expected 200 or 404): %s", resp.Status)
		return "", false, err
	default: // it's 200
		var consulRespArr []consulResponse
		err = json.Unmarshal(body, &consulRespArr)
		if (err != nil) || len(consulRespArr) != 1 {
			err := fmt.Errorf("Failed to unmarshal Consul response '%s', error: %s", body, err.Error())
			return "", false, err
		}

		consulResp := consulRespArr[0]

		cas = consulResp.ModifyIndex
		jinfo, err := base64.StdEncoding.DecodeString(consulResp.Value)
		if err != nil {
			err := fmt.Errorf("Failed to decode base64 value '%s', error: %s", consulResp.Value, err.Error())
			return "", false, err
		}

		var leaderInfo leaderInfo
		err = json.Unmarshal(jinfo, &leaderInfo)
		if err != nil {
			err := fmt.Errorf("Failed to decode leader info '%s', error: %s", jinfo, err.Error())
			return "", false, err
		}

		leaderId = leaderInfo.LeaderId
		if leaderId == conf.selfId {
			// leader always updates it's info
			update = true
		} else {
			// is it time to select a new leader?
			passed := time.Now().Sub(leaderInfo.UpdateTime)
			update = passed > conf.leaderHoldTime
		}
	}

	if !update {
		return leaderId, false, nil
	}

	info := leaderInfo{LeaderId: conf.selfId, UpdateTime: time.Now().UTC()}
	payload, _ := json.Marshal(info)
	url := conf.consulUrl + "?cas=" + strconv.FormatUint(cas, 10)
	resp, body, err = client.sendReq(http.MethodPut, url, payload)
	if err != nil {
		err := fmt.Errorf("PUT '%s' failed: '%s'", url, err.Error())
		return "", false, err
	}

	if resp.StatusCode != 200 {
		err := fmt.Errorf("Unexpected HTTP status code (expected 200): %s", resp.Status)
		return "", false, err
	}

	if string(body) != "true" {
		// CAS failed, repeat all over again
		return "", true, nil
	}

	// CAS succeeded
	return conf.selfId, false, nil
}

// Main function of the State Keeper process
func stateKeeperProc(reqch <-chan request) {
	leaderId := "" // not elected yet
	callbacks := []Callback{}

	for {
		req := <-reqch
		switch req.typetag {
		case tGETLEADER: // get current leader
			req.respch <- leaderId
		case tUPDATELEADER: // update current leader
			for i := 0; i < len(callbacks); i++ {
				callbacks[i](leaderId, req.newLeaderId)
			}
			leaderId = req.newLeaderId
		case tREGCALLBACK: // add a new callback
			callbacks = append(callbacks, req.callback)
		default:
			log.Panicf("State Keeper: unexpected request typetag %d\n", req.typetag)
		}
	}
}

// Main function of the State Updater process
func stateUpdaterProc(conf *electorConfig, updch chan<- request) {
	var err error
	var lastLeaderId string

	req := request{typetag: tUPDATELEADER}
	for {
		req.newLeaderId, err = getCurrentLeader(conf)
		if err != nil {
			// in this case req.newLeaderId is ''
			log.Printf("State Updater: unable to determine current leader: '%s'\n", err.Error())
		}

		if req.newLeaderId != lastLeaderId {
			updch <- req
			lastLeaderId = req.newLeaderId
		}

		// the random part guarantees that all the peers will not send requests to Consul simultaneously
		time.Sleep((conf.leaderHoldTime / 3) + time.Duration(rand.Intn(1000))*time.Millisecond)
	}
}

// Create an instance of the elector.
func Create(selfId, consulUrl string, leaderHoldTime time.Duration) (inst *Instance, err error) {
	if selfId == "" {
		err := fmt.Errorf("selfId should be a non-empty string")
		return nil, err
	}

	if leaderHoldTime <= 0 {
		err := fmt.Errorf("leaderHoldTime should be greater than zero")
		return nil, err
	}

	reqch := make(chan request)
	conf := &electorConfig{selfId: selfId, consulUrl: consulUrl, leaderHoldTime: leaderHoldTime}
	go stateKeeperProc(reqch)
	go stateUpdaterProc(conf, reqch)
	return &Instance{reqch: reqch}, nil
}

// Returns current leader id, or '' if leader is unknown.
func (inst *Instance) GetCurrentLeader() (leaderid string) {
	respch := make(chan string)
	req := request{typetag: tGETLEADER, respch: respch}
	inst.reqch <- req
	resp := <-respch
	return resp
}

// Registers a callback.
func (inst *Instance) RegisterCallback(cb Callback) {
	req := request{typetag: tREGCALLBACK, callback: cb}
	inst.reqch <- req
}
