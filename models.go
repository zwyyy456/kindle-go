package main

type Config struct {
	Kindle      KindleConfig
	SenseSource SenseSourceConfig
	AI          AIConfig
	Output      OutputConfig
}

type KindleConfig struct {
	VocabDB string
}

type SenseSourceConfig struct {
	Type             string
	DiscoveryPath    string
	SocketPath       string
	DictionaryPolicy string
}

type AIConfig struct {
	Backend       string
	Model         string
	CodexPath     string
	BatchSize     int
	MinConfidence float64
}

type OutputConfig struct {
	Type       string
	Path       string
	ReviewPath string
}

type KindleRecord struct {
	RequestID string
	Term      string
	Usage     string
	BookTitle string
	Location  string
	Timestamp int64
}

type FlashDictLookupRequest struct {
	RequestID string `json:"requestID"`
	Term      string `json:"term"`
	Usage     string `json:"usage,omitempty"`
}

type FlashDictLookupResponse struct {
	RequestID          string                    `json:"requestID"`
	Status             string                    `json:"status"`
	DictionaryPolicy   string                    `json:"dictionaryPolicy"`
	DictionaryStableID string                    `json:"dictionaryStableID"`
	DictName           string                    `json:"dictName"`
	Term               string                    `json:"term"`
	Candidates         []FlashDictSenseCandidate `json:"candidates"`
	Error              string                    `json:"error"`
}

type FlashDictSenseCandidate struct {
	CandidateID         string  `json:"candidateID"`
	DictionaryStableID  string  `json:"dictionaryStableID"`
	DictName            string  `json:"dictName"`
	Term                string  `json:"term"`
	HeaderHTML          *string `json:"headerHtml"`
	SubHeaderHTML       *string `json:"subHeaderHtml"`
	PhraseHeaderHTML    *string `json:"phraseHeaderHtml"`
	SenseHTML           string  `json:"senseHtml"`
	SenseIndex          *int    `json:"senseIndex"`
	SubsenseSelector    *string `json:"subsenseSelector"`
	ResourceTagsVersion int     `json:"resourceTagsVersion"`
	CSSTagsHTML         *string `json:"cssTagsHTML"`
	ScriptTagsHTML      *string `json:"scriptTagsHTML"`
}

type AIItem struct {
	RequestID  string        `json:"requestID"`
	Term       string        `json:"term"`
	Usage      string        `json:"usage"`
	Candidates []AICandidate `json:"candidates"`
}

type AICandidate struct {
	CandidateID string `json:"candidateID"`
	SenseHTML   string `json:"senseHtml"`
}

type AISelectionBatch struct {
	Results []AISelection `json:"results"`
}

type AISelection struct {
	RequestID           string  `json:"requestID"`
	SelectedCandidateID string  `json:"selectedCandidateID"`
	Confidence          float64 `json:"confidence"`
	Reason              string  `json:"reason"`
}

type ReviewRecord struct {
	RequestID  string                    `json:"requestID"`
	Term       string                    `json:"term"`
	Usage      string                    `json:"usage,omitempty"`
	BookTitle  string                    `json:"bookTitle,omitempty"`
	Location   string                    `json:"location,omitempty"`
	Reason     string                    `json:"reason"`
	Candidates []FlashDictSenseCandidate `json:"candidates,omitempty"`
	AI         *AISelection              `json:"ai,omitempty"`
	Error      string                    `json:"error,omitempty"`
}

type FlashcardExportEnvelope struct {
	SchemaVersion int                   `json:"schemaVersion"`
	ExportedAt    int64                 `json:"exportedAt"`
	Cards         []FlashcardExportCard `json:"cards"`
}

type FlashcardExportCard struct {
	CardID              string  `json:"cardID"`
	DictionaryStableID  string  `json:"dictionaryStableID"`
	Term                string  `json:"term"`
	DictName            string  `json:"dictName"`
	Sentence            string  `json:"sentence,omitempty"`
	SourceURL           *string `json:"sourceURL"`
	UserNote            *string `json:"userNote"`
	RenderMode          string  `json:"renderMode"`
	HeaderHTML          string  `json:"headerHtml"`
	SubHeaderHTML       *string `json:"subHeaderHtml"`
	PhraseHeaderHTML    *string `json:"phraseHeaderHtml"`
	SenseHTML           string  `json:"senseHtml"`
	EntryHTML           string  `json:"entryHtml"`
	ResourceTagsVersion int     `json:"resourceTagsVersion"`
	CSSTagsHTML         *string `json:"cssTagsHTML"`
	ScriptTagsHTML      *string `json:"scriptTagsHTML"`
	SenseIndex          int     `json:"senseIndex"`
	SubsenseSelector    *string `json:"subsenseSelector"`
	LearningState       string  `json:"learningState"`
	CreatedAt           int64   `json:"createdAt"`
	UpdatedAt           int64   `json:"updatedAt"`
}

type Summary struct {
	RawRecords          int
	SkippedMissingUsage int
	DedupedRecords      int
	LookupFailures      int
	AILowConfidence     int
	AIInvalid           int
	Cards               int
	ReviewRecords       int
}
