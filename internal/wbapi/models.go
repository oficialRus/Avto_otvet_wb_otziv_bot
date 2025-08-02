package wbapi

import "time"

// Feedback represents a single customer review fetched from WB API.
// Only the fields required by our service are included, but additional
// JSON tags can be added later if needed.
// The official API uses strings for some IDs; to avoid precision loss we
// keep ID as string.
// Doc: https://dev.wildberries.ru/en/openapi/user-communication#/Feedbacks/get_feedbacks
type Feedback struct {
	ID               string    `json:"id"`
	Text             string    `json:"text"`
	Pros             string    `json:"pros"`
	Cons             string    `json:"cons"`
	ProductValuation int       `json:"productValuation"` // 1–5 stars
	CreatedDate      time.Time `json:"createdDate"`
	WasViewed        bool      `json:"wasViewed"`
	IsWarned         bool      `json:"isWarned"`
}

// feedbacksListData is the "data" envelope inside the list response.
// Only fields we actually use are mapped.
// {
//   "data": {
//     "countUnanswered": 52,
//     "feedbacks": [ ... ]
//   },
//   "error": false,
//   "errorText": "",
//   "additionalErrors": null
// }
type feedbacksListData struct {
	CountUnanswered int        `json:"countUnanswered"`
	Feedbacks       []Feedback `json:"feedbacks"`
}

// feedbacksListResp is the top‑level response for GET /feedbacks
type feedbacksListResp struct {
	Data             feedbacksListData `json:"data"`
	Error            bool              `json:"error"`
	ErrorText        string            `json:"errorText"`
	AdditionalErrors interface{}       `json:"additionalErrors"`
}

// answerRequest is the body for POST /feedbacks/answer
// Example:
//   { "id": "YX52RZEBhH9mrcYdEJuD", "text": "Thank you!" }
// Note: API may also accept questionId but for feedbacks we only need id.
type answerRequest struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// genericResponse captures the common error envelope returned by WB.
type genericResponse struct {
	Data             interface{} `json:"data"`
	Error            bool        `json:"error"`
	ErrorText        string      `json:"errorText"`
	AdditionalErrors interface{} `json:"additionalErrors"`
}
