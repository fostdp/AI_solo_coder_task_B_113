package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type TransportClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewTransportClient(serviceAddr string) *TransportClient {
	return &TransportClient{
		baseURL:    fmt.Sprintf("http://%s", serviceAddr),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *TransportClient) Score(req ScoreRequest) (*ScoreResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", c.baseURL+"/v1/score", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return c.basicFallback(req), nil
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result ScoreResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return c.basicFallback(req), nil
	}
	return &result, nil
}

func (c *TransportClient) ScoreBatch(requests []ScoreRequest) ([]*ScoreResponse, error) {
	body, _ := json.Marshal(requests)
	httpReq, _ := http.NewRequest("POST", c.baseURL+"/v1/score/batch", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		results := make([]*ScoreResponse, len(requests))
		for i, req := range requests {
			results[i] = c.basicFallback(req)
		}
		return results, nil
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var results []*ScoreResponse
	if err := json.Unmarshal(data, &results); err != nil {
		fallback := make([]*ScoreResponse, len(requests))
		for i, req := range requests {
			fallback[i] = c.basicFallback(req)
		}
		return fallback, nil
	}
	return results, nil
}

func (c *TransportClient) GetStats() (*StatsResponse, error) {
	httpReq, _ := http.NewRequest("GET", c.baseURL+"/v1/stats", nil)
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return &StatsResponse{
			TotalScores: 0,
			AvgScore:    0,
			LevelCounts: map[string]int64{"low": 0, "medium": 0, "high": 0, "critical": 0},
		}, nil
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var stats StatsResponse
	if err := json.Unmarshal(data, &stats); err != nil {
		return &StatsResponse{
			TotalScores: 0,
			AvgScore:    0,
			LevelCounts: map[string]int64{"low": 0, "medium": 0, "high": 0, "critical": 0},
		}, nil
	}
	return &stats, nil
}

func (c *TransportClient) basicFallback(req ScoreRequest) *ScoreResponse {
	score := 50
	level := "medium"
	if req.Urgent {
		score += 20
	}
	if req.Priority >= 3 {
		score += 10
	}
	if score >= 80 {
		level = "critical"
	} else if score >= 60 {
		level = "high"
	} else if score < 30 {
		level = "low"
	}
	return &ScoreResponse{
		RequestID:        req.RequestID,
		RiskScore:        int32(score),
		RiskLevel:        level,
		AdverseEventProb: float64(score) / 100.0 * 0.4,
		FeatureContrib:   map[string]float64{},
		Recommendations:  []string{"基础降级评分，建议人工复核"},
		UsedDefaultDist:  req.Distance <= 0,
		UsedDefaultVitals: req.VitalStability < 0 || req.InfectionRisk < 0,
		ScoredAtUnix:     time.Now().Unix(),
		Success:          true,
	}
}
