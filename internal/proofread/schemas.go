package proofread

import _ "embed"

//go:embed schemas/first_review.json
var FirstReviewSchema []byte

//go:embed schemas/verification.json
var VerificationSchema []byte

//go:embed schemas/image_review.json
var ImageReviewSchema []byte
