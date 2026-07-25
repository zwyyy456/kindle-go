package generation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

const taskParametersVersion = 1

func decodeParameters(raw string) (Parameters, error) {
	var header struct {
		SchemaVersion int `json:"version"`
	}
	if err := json.Unmarshal([]byte(raw), &header); err != nil {
		return Parameters{}, fmt.Errorf("decode generation task parameters: %w", err)
	}
	version := header.SchemaVersion
	if version == 0 {
		// Libraries created before task parameter versioning have no version
		// field. They use the v1 shape and remain readable for retry/recovery.
		version = taskParametersVersion
	}
	if version != taskParametersVersion {
		return Parameters{}, fmt.Errorf("unsupported generation task parameters version %d", version)
	}
	var params Parameters
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&params); err != nil {
		return Parameters{}, fmt.Errorf("decode generation task parameters v%d: %w", version, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Parameters{}, fmt.Errorf("decode generation task parameters v%d: trailing JSON value", version)
	}
	params.SchemaVersion = version
	return params, nil
}
