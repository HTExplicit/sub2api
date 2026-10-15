package service

// PelicanResult contains one ordinary-account generation observation.
type PelicanResult struct {
	RawAnswer   string `json:"raw_answer"`
	RawResponse string `json:"raw_response"`
	ModelID     string `json:"model_id"`
	Effort      string `json:"effort"`
	Status      string `json:"status"`
	Error       string `json:"error"`
}

const pelicanMaxBody = 32 << 20
