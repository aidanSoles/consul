package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hashicorp/consul/agent/structs"
	"github.com/mitchellh/mapstructure"
)

// Config switches on the different CRUD operations for config entries.
func (s *HTTPServer) Config(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
	switch req.Method {
	case "GET":
		return s.configGet(resp, req)

	case "DELETE":
		return s.configDelete(resp, req)

	default:
		return nil, MethodNotAllowedError{req.Method, []string{"GET", "DELETE"}}
	}
}

// configGet gets either a specific config entry, or lists all config entries
// of a kind if no name is provided.
func (s *HTTPServer) configGet(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
	var args structs.ConfigEntryQuery
	if done := s.parse(resp, req, &args.Datacenter, &args.QueryOptions); done {
		return nil, nil
	}
	pathArgs := strings.SplitN(strings.TrimPrefix(req.URL.Path, "/v1/config/"), "/", 2)

	switch len(pathArgs) {
	case 2:
		// Both kind/name provided.
		args.Kind = pathArgs[0]
		args.Name = pathArgs[1]

		var reply structs.IndexedConfigEntries
		if err := s.agent.RPC("ConfigEntry.Get", &args, &reply); err != nil {
			return nil, err
		}

		return reply, nil
	case 1:
		// Only kind provided, list entries.
		args.Kind = pathArgs[0]

		var reply structs.IndexedConfigEntries
		if err := s.agent.RPC("ConfigEntry.List", &args, &reply); err != nil {
			return nil, err
		}

		return reply, nil
	default:
		resp.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(resp, "Must provide either a kind or both kind and name")
		return nil, nil
	}
}

// configDelete deletes the given config entry.
func (s *HTTPServer) configDelete(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
	var args structs.ConfigEntryRequest
	s.parseDC(req, &args.Datacenter)
	s.parseToken(req, &args.Token)
	pathArgs := strings.SplitN(strings.TrimPrefix(req.URL.Path, "/v1/config/"), "/", 2)

	if len(pathArgs) != 2 {
		resp.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(resp, "Must provide both a kind and name to delete")
		return nil, nil
	}

	entry, err := structs.MakeConfigEntry(pathArgs[0], pathArgs[1])
	if err != nil {
		resp.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(resp, "%v", err)
		return nil, nil
	}
	args.Entry = entry

	var reply struct{}
	if err := s.agent.RPC("ConfigEntry.Delete", &args, &reply); err != nil {
		return nil, err
	}

	return reply, nil
}

// decodeBody is used to decode a JSON request body
func decodeConfigBody(req *http.Request) (structs.ConfigEntry, error) {
	// This generally only happens in tests since real HTTP requests set
	// a non-nil body with no content. We guard against it anyways to prevent
	// a panic. The EOF response is the same behavior as an empty reader.
	if req.Body == nil {
		return nil, io.EOF
	}

	var raw map[string]interface{}
	dec := json.NewDecoder(req.Body)
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}

	var entry structs.ConfigEntry

	kindVal, ok := raw["Kind"]
	if !ok {
		kindVal, ok = raw["kind"]
	}
	if !ok {
		return nil, fmt.Errorf("Payload does not contain a kind/Kind key at the top level")
	}

	if kindStr, ok := kindVal.(string); ok {
		newEntry, err := structs.MakeConfigEntry(kindStr, "")
		if err != nil {
			return nil, err
		}
		entry = newEntry
	} else {
		return nil, fmt.Errorf("Kind value in payload is not a string")
	}

	decodeConf := &mapstructure.DecoderConfig{
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			stringToReadableDurationFunc(),
		),
		Result: &entry,
	}

	decoder, err := mapstructure.NewDecoder(decodeConf)
	if err != nil {
		return nil, err
	}

	return entry, decoder.Decode(raw)
}

func decodeConfigEntry(raw map[string]interface{}) (structs.ConfigEntry, error) {
	var entry structs.ConfigEntry

	kindVal, ok := raw["Kind"]
	if !ok {
		kindVal, ok = raw["kind"]
	}
	if !ok {
		return nil, fmt.Errorf("Payload does not contain a kind/Kind key at the top level")
	}

	if kindStr, ok := kindVal.(string); ok {
		newEntry, err := structs.MakeConfigEntry(kindStr, "")
		if err != nil {
			return nil, err
		}
		entry = newEntry
	} else {
		return nil, fmt.Errorf("Kind value in payload is not a string")
	}

	decodeConf := &mapstructure.DecoderConfig{
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
			stringToReadableDurationFunc(),
		),
		Result: &entry,
	}

	decoder, err := mapstructure.NewDecoder(decodeConf)
	if err != nil {
		return nil, err
	}

	return entry, decoder.Decode(raw)
}

// ConfigCreate applies the given config entry update.
func (s *HTTPServer) ConfigApply(resp http.ResponseWriter, req *http.Request) (interface{}, error) {
	args := structs.ConfigEntryRequest{
		Op: structs.ConfigEntryUpsert,
	}
	s.parseDC(req, &args.Datacenter)
	s.parseToken(req, &args.Token)

	var raw map[string]interface{}
	if err := decodeBody(req, &raw, nil); err != nil {
		return nil, BadRequestError{Reason: fmt.Sprintf("Request decoding failed: %v", err)}
	}

	if entry, err := decodeConfigEntry(raw); err == nil {
		args.Entry = entry
	} else {
		return nil, BadRequestError{Reason: fmt.Sprintf("Request decoding failed: %v", err)}
	}

	var reply struct{}
	return nil, s.agent.RPC("ConfigEntry.Apply", &args, &reply)
}