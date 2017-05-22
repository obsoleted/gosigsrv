package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// TestCommonMiddleware tests that the middleware adds the right headers
func TestCommonMiddleware(t *testing.T) {
	req, err := http.NewRequest("GET", "/test", nil)
	if err != nil {
		t.Fatal(err)
	}

	expectedHeaders := make(map[string]string)
	expectedHeaders["Access-Control-Allow-Origin"] = "*"
	expectedHeaders["Access-Control-Allow-Credentials"] = "true"
	expectedHeaders["Access-Control-Allow-Methods"] = strings.Join([]string{"GET", "POST", "OPTIONS"}, ",")
	expectedHeaders["Access-Control-Allow-Headers"] = strings.Join([]string{"Content-Type", "Content-Length", "Cache-Control", "Connection"}, ",")
	expectedHeaders["Access-Control-Expose-Headers"] = strings.Join([]string{"Content-Length", "X-Peer-Id"}, ",")
	expectedHeaders["Connection"] = "close"
	expectedHeaders["Cache-Control"] = "no-cache"

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for headerKey, expectedHeaderValue := range expectedHeaders {
			actualHeaderValue := w.Header().Get(headerKey)
			t.Logf("Checkin %s ... ", headerKey)
			if actualHeaderValue != expectedHeaderValue {
				t.Errorf("Header '%s' is wrong. Expected '%s' Actual '%s'", headerKey, expectedHeaderValue, actualHeaderValue)
			}
		}
	})

	rr := httptest.NewRecorder()
	// func RequestIDMiddleware(h http.Handler) http.Handler
	// Stores an "app.req.id" in the request context.
	handler := commonHeaderMiddleware(testHandler)
	handler.ServeHTTP(rr, req)
}

func TestSignInOk(t *testing.T) {
	const expectedPeerName string = "peername"
	queryParams := make(url.Values)
	queryParams.Add(expectedPeerName, "")

	req, err := http.NewRequest("GET", "/sign_in?"+queryParams.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	signInHandler := http.HandlerFunc(signinHandler)
	signInHandler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Recieved wrong status code expected %v, got %v", http.StatusOK, status)
	}

	pragmaValues, pragmaExists := rr.HeaderMap["Pragma"]
	if !pragmaExists {
		t.Errorf("Sign in response did not contain Pragma header")
	}
	var pragma string
	if len(pragmaValues) > 0 {
		pragma = pragmaValues[0]
	}

	responseBodyString := string(rr.Body.Bytes())
	if commaCount := strings.Count(responseBodyString, ","); commaCount != 2 {
		t.Errorf("Response is not formatted correctly: %s", responseBodyString)
	} else {
		splitResponse := strings.Split(responseBodyString, ",")
		actualPeerName := splitResponse[0]
		peerIDFromBody := splitResponse[1]
		if peerIDFromBody != pragma {
			t.Errorf("Pragma (%s) and Peer id in response (%s) do not match", pragma, peerIDFromBody)
		}

		if actualPeerName != expectedPeerName {
			t.Errorf("Got incorrect peername Expected %s Recieved %s", expectedPeerName, actualPeerName)
		}
	}
}

func signIn(t *testing.T, peername string) (id string, err error) {
	queryParams := make(url.Values)
	queryParams.Add(peername, "")

	req, err := http.NewRequest("GET", "/sign_in?"+queryParams.Encode(), nil)
	if err != nil {
		return "", err
	}

	rr := httptest.NewRecorder()
	signInHandler := http.HandlerFunc(signinHandler)
	signInHandler.ServeHTTP(rr, req)

	pragmaValues, _ := rr.HeaderMap["Pragma"]
	var pragma string
	if len(pragmaValues) > 0 {
		pragma = pragmaValues[0]
	}

	return pragma, nil
}

func TestSignOutOk(t *testing.T) {
	const expectedPeerName string = "peername"

	peerID, err := signIn(t, expectedPeerName)
	if err != nil {
		t.Errorf("Error signing in %v", err)
		t.Fatal(err)
	}

	queryParams := make(url.Values)
	queryParams.Add("peer_id", peerID)

	t.Log(queryParams.Encode())

	req, err := http.NewRequest("GET", "/sign_out?"+queryParams.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	signoutHandler := http.HandlerFunc(signoutHandler)
	signoutHandler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Recieved wrong status code expected %v, got %v", http.StatusOK, status)
	}

	pragmaValues, pragmaExists := rr.HeaderMap["Pragma"]
	if !pragmaExists {
		t.Errorf("Sign in response did not contain Pragma header")
	}
	var pragma string
	if len(pragmaValues) > 0 {
		pragma = pragmaValues[0]
	}
	if pragma != peerID {
		t.Errorf("Peer id (%s) does not match Pragma value in response (%s)", peerID, pragma)
	}
}

func TestSignOutFailsWithBadPeerId(t *testing.T) {
	const invalidPeerID string = "invalid"

	queryParams := make(url.Values)
	queryParams.Add("peer_id", invalidPeerID)

	t.Log(queryParams.Encode())

	req, err := http.NewRequest("GET", "/sign_out?"+queryParams.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	signoutHandler := http.HandlerFunc(signoutHandler)
	signoutHandler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Recieved wrong status code expected %v, got %v", http.StatusOK, status)
	}
}

func TestSendMessage(t *testing.T) {
	var err error
	var peerA, peerB string
	const peerAname string = "client_peerA"
	const peerBname string = "renderingserver_peerB"
	peerA, err = signIn(t, peerAname)
	if err != nil {
		t.Fatal(err)
	}

	peerB, err = signIn(t, peerBname)
	if err != nil {
		t.Fatal(err)
	}

	queryParams := make(url.Values)
	queryParams.Add("peer_id", peerA)
	queryParams.Add("to", peerB)

	messageBody := bytes.NewReader([]byte("{\"arbitrary\": \"value\"}"))

	req, err := http.NewRequest("POST", "/message?"+queryParams.Encode(), messageBody)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	messageHandler := http.HandlerFunc(messageHandler)
	messageHandler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Errorf("Recieved wrong status code expected %v, got %v", http.StatusOK, status)
	}

	pragmaValues, pragmaExists := rr.HeaderMap["Pragma"]
	if !pragmaExists {
		t.Errorf("Sign in response did not contain Pragma header")
	}

	var pragma string
	if len(pragmaValues) > 0 {
		pragma = pragmaValues[0]
	}

	if pragma != peerA {
		t.Errorf("Peer ID (%s) and Pragma value (%s) do not match", peerA, pragma)
	}
}

func TestReceieveMessage(t *testing.T) {
	var err error
	var peerA, peerB string
	const peerAname string = "client_peerA"
	const peerBname string = "renderingserver_peerB"
	const expectedMessageContent = "{\"arbitrary\": \"value\"}"
	peerA, err = signIn(t, peerAname)
	if err != nil {
		t.Fatal(err)
	}

	peerB, err = signIn(t, peerBname)
	if err != nil {
		t.Fatal(err)
	}

	queryParams := make(url.Values)
	queryParams.Add("peer_id", peerA)
	queryParams.Add("to", peerB)

	messageBody := bytes.NewReader([]byte("{\"arbitrary\": \"value\"}"))

	req, err := http.NewRequest("POST", "/message?"+queryParams.Encode(), messageBody)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	messageHandler := http.HandlerFunc(messageHandler)
	messageHandler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("Recieved wrong status code expected %v, got %v", http.StatusOK, status)
	}

	// Get message
	queryParams = make(url.Values)
	queryParams.Add("peer_id", peerB)

	req, err = http.NewRequest("GET", "/wait?"+queryParams.Encode(), messageBody)
	if err != nil {
		t.Fatal(err)
	}

	rr = httptest.NewRecorder()
	waitHandler := http.HandlerFunc(waitHandler)
	waitHandler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusOK {
		t.Fatalf("Recieved wrong status code expected %v, got %v", http.StatusOK, status)
	}

	contentLength, converr := strconv.Atoi(rr.Header().Get("Content-Length"))
	if converr != nil {
		t.Errorf("Content length could not be cast to int")
	}

	if contentLength != rr.Body.Len() {
		t.Errorf("Content length header (%d) did not match actual content length (%d)", contentLength, rr.Body.Len())
	}

	responseBodyString := string(rr.Body.Bytes())
	if responseBodyString != expectedMessageContent {
		t.Errorf("Message recieved (%s) are different than what was sent (%s)", responseBodyString, expectedMessageContent)
	}

	pragmaValues, pragmaExists := rr.HeaderMap["Pragma"]
	if !pragmaExists {
		t.Errorf("Sign in response did not contain Pragma header")
	}

	var pragma string
	if len(pragmaValues) > 0 {
		pragma = pragmaValues[0]
	}

	if pragma != peerA {
		t.Errorf("Peer ID of sender (%s) and Pragma value (%s) do not match", peerA, pragma)
	}
}

func TestSignInFailsWithoutPerrname(t *testing.T) {
	queryParams := make(url.Values)
	req, err := http.NewRequest("GET", "/sign_in?"+queryParams.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	signInHandler := http.HandlerFunc(signinHandler)
	signInHandler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Recieved wrong status code expected %v, got %v", http.StatusOK, status)
	}
}
