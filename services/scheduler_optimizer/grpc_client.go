package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

type SchedulerClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewSchedulerClient(serviceAddr string) *SchedulerClient {
	return &SchedulerClient{
		baseURL:    fmt.Sprintf("http://%s", serviceAddr),
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *SchedulerClient) Solve(req SolveRequest) (*SolveResponse, error) {
	if req.DeadlineMs > 0 {
		c.httpClient.Timeout = time.Duration(req.DeadlineMs+500) * time.Millisecond
		defer func() { c.httpClient.Timeout = 15 * time.Second }()
	}

	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", c.baseURL+"/v1/solve", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return c.greedyFallback(req), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return c.greedyFallback(req), nil
	}

	data, _ := io.ReadAll(resp.Body)
	var result SolveResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return c.greedyFallback(req), nil
	}
	return &result, nil
}

func (c *SchedulerClient) greedyFallback(req SolveRequest) *SolveResponse {
	bedRisksMap := make(map[uint32]float64)
	beds := make([]uint32, 0, len(req.BedRisks))
	for _, br := range req.BedRisks {
		bedRisksMap[br.BedID] = br.InfectionRisk
		beds = append(beds, br.BedID)
	}

	if len(beds) == 0 {
		for i := uint32(1); i <= 50; i++ {
			beds = append(beds, i)
			if _, exists := bedRisksMap[i]; !exists {
				bedRisksMap[i] = 0.5
			}
		}
	}

	npCapacity := int(req.NegativePressureBeds)
	if npCapacity <= 0 {
		npCapacity = 10
	}

	type bedRiskPair struct {
		bedID uint32
		risk  float64
	}
	pairs := make([]bedRiskPair, 0, len(beds))
	for _, bedID := range beds {
		pairs = append(pairs, bedRiskPair{bedID: bedID, risk: bedRisksMap[bedID]})
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i].risk > pairs[j].risk
	})

	assignments := make(map[uint32]string)
	npCount := 0
	wardCount := 0
	for _, pair := range pairs {
		if npCount < npCapacity {
			npCount++
			assignments[pair.bedID] = fmt.Sprintf("NP-%03d", npCount)
		} else {
			wardCount++
			assignments[pair.bedID] = fmt.Sprintf("WARD-%03d", wardCount)
		}
	}

	return &SolveResponse{
		SolutionID:       fmt.Sprintf("SOL-FALLBACK-%d", time.Now().UnixMilli()),
		Assignments:      assignments,
		Schedule:         make(map[string][]uint32),
		Objective:        make(map[string]float64),
		UnmetNeeds:       make([]string, 0),
		Suggestions:      make([]Suggestion, 0),
		Status:           "fallback",
		ChangeRate:       0.3,
		SolveTimeMs:      0,
		IsGreedyFallback: true,
	}
}

func (c *SchedulerClient) GetChangeRate() (*ChangeRateResponse, error) {
	httpReq, _ := http.NewRequest("GET", c.baseURL+"/v1/stats/change_rate", nil)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	var result ChangeRateResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *SchedulerClient) SetStabilityLambda(lambda float64) (*AckResponse, error) {
	reqBody, _ := json.Marshal(LambdaRequest{Lambda: lambda})
	httpReq, _ := http.NewRequest("POST", c.baseURL+"/v1/lambda", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	var result AckResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *SchedulerClient) GetSuggestions(prevID, currID string) (*SuggestResponse, error) {
	reqBody, _ := json.Marshal(SuggestRequest{
		PreviousSolutionID: prevID,
		CurrentSolutionID:  currID,
	})
	httpReq, _ := http.NewRequest("POST", c.baseURL+"/v1/suggestions", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	var result SuggestResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *SchedulerClient) HealthCheck() (bool, error) {
	httpReq, _ := http.NewRequest("GET", c.baseURL+"/health", nil)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200, nil
}
