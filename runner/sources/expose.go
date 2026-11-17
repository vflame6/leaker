package sources

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/vflame6/leaker/logger"
	"github.com/vflame6/leaker/utils"
)

// Expose queries the Expose people and company search API.
// Each request uses the caller's own key and spends one Expose credit.
// Email and phone search a person. Domain searches a company.
// Username and keyword are not sent: Expose has no search for them.
type Expose struct {
	apiKeys []string

	// baseURL overrides the API root (tests only).
	baseURL string
}

type exposeRecord struct {
	ID          json.RawMessage    `json:"id"`
	Domain      string             `json:"domain"`
	Emails      []exposeEmail      `json:"emails"`
	Phones      []exposePhone      `json:"phones"`
	Names       []exposeName       `json:"names"`
	Profiles    []exposeProfile    `json:"profiles"`
	Locations   []exposeLocation   `json:"locations"`
	Experiences []exposeExperience `json:"experiences"`
	Websites    []exposeWebsite    `json:"websites"`
}

type exposeEmail struct {
	Address string `json:"address"`
}

type exposePhone struct {
	Number string `json:"number"`
}

type exposeName struct {
	First  nullString `json:"first_name"`
	Middle nullString `json:"middle_name"`
	Last   nullString `json:"last_name"`
}

// nullString unmarshals a JSON string or null. Expose name parts are often null.
type nullString struct {
	Value string
}

func (n *nullString) UnmarshalJSON(data []byte) error {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 || string(data) == "null" {
		n.Value = ""
		return nil
	}
	return json.Unmarshal(data, &n.Value)
}

type exposeProfile struct {
	Username string `json:"username"`
	URL      string `json:"url"`
	Network  string `json:"network"`
}

type exposeLocation struct {
	City    string `json:"city"`
	State   string `json:"state"`
	Country string `json:"country"`
}

type exposeExperience struct {
	CompanyName string `json:"company_name"`
}

// exposeWebsite accepts either a URL string or an object with a url field.
type exposeWebsite struct {
	URL string `json:"url"`
}

func (w *exposeWebsite) UnmarshalJSON(data []byte) error {
	data = []byte(strings.TrimSpace(string(data)))
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		return json.Unmarshal(data, &w.URL)
	}
	var obj struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	w.URL = obj.URL
	return nil
}

func (s *Expose) Run(ctx context.Context, target string, scanType ScanType, session *Session) <-chan Result {
	results := make(chan Result)

	go func() {
		defer close(results)

		endpoint, ok := s.endpoint(scanType, target)
		if !ok {
			return
		}
		apiKey := utils.PickRandom(s.apiKeys, s.Name(), s.NeedsKey())
		if apiKey == "" {
			return
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("Accept", "application/json")

		logger.Debugf("Sending a request in Expose source for %s", target)
		resp, err := session.Client.Do(req)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		defer session.DiscardHTTPResponse(resp)

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		logger.Debugf("Response from Expose source: status code [%d], size [%d]", resp.StatusCode, len(body))

		if resp.StatusCode != http.StatusOK {
			results <- Result{Source: s.Name(), Error: exposeHTTPError(resp.StatusCode, body)}
			return
		}

		records, err := parseExposeRecords(body)
		if err != nil {
			results <- Result{Source: s.Name(), Error: err}
			return
		}
		for _, record := range records {
			if result, ok := s.toResult(target, scanType, record); ok {
				results <- result
			}
		}
	}()

	return results
}

func (s *Expose) endpoint(scanType ScanType, target string) (string, bool) {
	query := url.Values{}
	switch scanType {
	case TypeEmail:
		query.Set("type", "person")
		query.Set("email", target)
	case TypePhone:
		query.Set("type", "person")
		query.Set("phone", target)
	case TypeDomain:
		query.Set("type", "company")
		query.Set("domain", target)
	default:
		return "", false
	}
	return s.root() + "/api/search?" + query.Encode(), true
}

func (s *Expose) root() string {
	if s.baseURL != "" {
		return strings.TrimRight(s.baseURL, "/")
	}
	return "https://expose.team"
}

func exposeHTTPError(status int, body []byte) error {
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("Expose rejected the API key")
	case http.StatusPaymentRequired:
		return fmt.Errorf("Expose account is out of credits")
	default:
		snippet := exposeSnippet(body)
		if snippet == "" {
			return fmt.Errorf("Expose returned status %d", status)
		}
		return fmt.Errorf("Expose returned status %d: %s", status, snippet)
	}
}

func exposeSnippet(body []byte) string {
	text := strings.Join(strings.Fields(string(body)), " ")
	if len(text) > 180 {
		text = text[:180]
	}
	return text
}

func parseExposeRecords(body []byte) ([]exposeRecord, error) {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("Expose returned an invalid response")
	}
	raw := []byte(strings.TrimSpace(string(envelope.Data)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	switch raw[0] {
	case '[':
		var list []exposeRecord
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("Expose returned an invalid response")
		}
		return list, nil
	case '{':
		var one exposeRecord
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, fmt.Errorf("Expose returned an invalid response")
		}
		return []exposeRecord{one}, nil
	default:
		return nil, fmt.Errorf("Expose returned an unexpected payload")
	}
}

func (s *Expose) toResult(target string, scanType ScanType, record exposeRecord) (Result, bool) {
	if !record.found() {
		return Result{}, false
	}

	result := Result{Source: s.Name(), Database: "Expose"}
	if len(record.Names) > 0 {
		result.Name = joinExposeName(record.Names[0])
	}

	emails := exposeEmails(record.Emails)
	phones := exposePhones(record.Phones)
	profileURL, username, network := firstExposeProfile(record.Profiles)
	website := ""
	if len(record.Websites) > 0 {
		website = exposeURL(record.Websites[0].URL)
	}

	switch scanType {
	case TypeEmail:
		if match := firstContaining(target, emails...); match != "" {
			result.Email = match
		} else {
			result.Email = target
		}
		if len(phones) > 0 {
			result.Phone = phones[0]
		}
		result.URL = firstNonEmpty(profileURL, website)
	case TypePhone:
		if match := firstContaining(target, phones...); match != "" {
			result.Phone = match
		} else {
			result.Phone = target
		}
		if len(emails) > 0 {
			result.Email = emails[0]
		}
		result.URL = firstNonEmpty(profileURL, website)
	case TypeDomain:
		if match := firstContaining(target, emails...); match != "" {
			result.Email = match
		}
		if len(phones) > 0 {
			result.Phone = phones[0]
		}
		result.URL = firstContaining(target, website, profileURL, exposeURL(record.Domain))
		if result.URL == "" {
			result.URL = "https://" + strings.TrimSpace(target)
		}
	}

	result.Username = username
	if location := firstExposeLocation(record.Locations); location != "" {
		result.SetExtra("location", location)
	}
	if company := firstExposeCompany(record.Experiences); company != "" {
		result.SetExtra("company", company)
	}
	if network != "" {
		result.SetExtra("network", network)
	}
	if !result.HasData() {
		return Result{}, false
	}
	return result, true
}

func (record exposeRecord) found() bool {
	if exposeIDPresent(record.ID) {
		return true
	}
	return len(record.Emails) > 0 || len(record.Phones) > 0 || len(record.Names) > 0 ||
		len(record.Profiles) > 0 || len(record.Websites) > 0 || strings.TrimSpace(record.Domain) != ""
}

func exposeIDPresent(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null" && value != "0" && value != `""`
}

func exposeEmails(emails []exposeEmail) []string {
	var out []string
	for _, email := range emails {
		address := strings.TrimSpace(email.Address)
		if address != "" {
			out = append(out, address)
		}
	}
	return out
}

func exposePhones(phones []exposePhone) []string {
	var out []string
	for _, phone := range phones {
		number := strings.TrimSpace(phone.Number)
		if number != "" {
			out = append(out, number)
		}
	}
	return out
}

func firstExposeProfile(profiles []exposeProfile) (profileURL, username, network string) {
	for _, profile := range profiles {
		username = strings.TrimSpace(profile.Username)
		network = strings.TrimSpace(profile.Network)
		profileURL = exposeURL(profile.URL)
		if profileURL != "" || username != "" || network != "" {
			return profileURL, username, network
		}
	}
	return "", "", ""
}

func firstExposeLocation(locations []exposeLocation) string {
	for _, location := range locations {
		parts := exposeParts(location.City, location.State, location.Country)
		if len(parts) > 0 {
			return strings.Join(parts, ", ")
		}
	}
	return ""
}

func firstExposeCompany(experiences []exposeExperience) string {
	for _, experience := range experiences {
		name := strings.TrimSpace(experience.CompanyName)
		if name != "" {
			return name
		}
	}
	return ""
}

func joinExposeName(name exposeName) string {
	return strings.Join(exposeParts(name.First.Value, name.Middle.Value, name.Last.Value), " ")
}

func exposeParts(values ...string) []string {
	var parts []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	return parts
}

func exposeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		return raw
	}
	return "https://" + raw
}

func firstContaining(target string, values ...string) string {
	needle := strings.ToLower(strings.TrimSpace(target))
	if needle == "" {
		return ""
	}
	for _, value := range values {
		if value != "" && strings.Contains(strings.ToLower(value), needle) {
			return value
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s *Expose) Name() string {
	return "expose"
}

func (s *Expose) UsesKey() bool {
	return true
}

func (s *Expose) NeedsKey() bool {
	return true
}

func (s *Expose) AddApiKeys(keys []string) {
	s.apiKeys = keys
}

// RateLimit stays at one request per second. Each search spends one credit.
func (s *Expose) RateLimit() int {
	return 1
}
