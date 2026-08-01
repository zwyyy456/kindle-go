package proofread

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const taskParametersVersion = 1

// ParseRevisionParameters strictly decodes a persisted revision task snapshot.
func ParseRevisionParameters(raw string) (RevisionParameters, error) {
	return decodeRevisionParameters(raw)
}

func decodeProofreadParameters(raw string) (Parameters, error) {
	var params Parameters
	version, err := decodeVersionedParameters(raw, "proofread", &params)
	if err != nil {
		return Parameters{}, err
	}
	params.SchemaVersion = version
	return params, nil
}

func decodeRevisionParameters(raw string) (RevisionParameters, error) {
	var params RevisionParameters
	version, err := decodeVersionedParameters(raw, "revision", &params)
	if err != nil {
		return RevisionParameters{}, err
	}
	params.SchemaVersion = version
	return params, nil
}

func decodeVersionedParameters(raw, kind string, target any) (int, error) {
	var header struct {
		SchemaVersion int `json:"version"`
	}
	if err := json.Unmarshal([]byte(raw), &header); err != nil {
		return 0, fmt.Errorf("decode %s task parameters: %w", kind, err)
	}
	version := header.SchemaVersion
	if version == 0 {
		// Libraries created before task parameter versioning have no version
		// field. They use the v1 shape and remain readable for retry/recovery.
		version = taskParametersVersion
	}
	if version != taskParametersVersion {
		return 0, fmt.Errorf("unsupported %s task parameters version %d", kind, version)
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return 0, fmt.Errorf("decode %s task parameters v%d: %w", kind, version, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return 0, fmt.Errorf("decode %s task parameters v%d: trailing JSON value", kind, version)
	}
	return version, nil
}
