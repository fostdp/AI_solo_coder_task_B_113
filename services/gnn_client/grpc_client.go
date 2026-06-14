package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type GnnClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewGnnClient(serviceAddr string) *GnnClient {
	return &GnnClient{
		baseURL: fmt.Sprintf("http://%s", serviceAddr),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

func (c *GnnClient) PredictSpread(req PredictSpreadRequest) (*PredictSpreadResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", c.baseURL+"/v1/predict", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return c.fallbackResponse(req), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return c.fallbackResponse(req), nil
	}

	data, _ := io.ReadAll(resp.Body)
	var result PredictSpreadResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return c.fallbackResponse(req), nil
	}
	return &result, nil
}

func (c *GnnClient) PredictSpreadWithTimeout(req PredictSpreadRequest, timeoutMs int64) (*PredictSpreadResponse, error) {
	c.httpClient.Timeout = time.Duration(timeoutMs) * time.Millisecond
	defer func() { c.httpClient.Timeout = 5 * time.Second }()
	return c.PredictSpread(req)
}

func (c *GnnClient) UpdateCultureResult(req CultureRequest) (*AckResponse, error) {
	body, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest("POST", c.baseURL+"/v1/culture", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return &AckResponse{
			Success: false,
			Message: "服务不可达: " + err.Error(),
		}, nil
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result AckResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return &AckResponse{
			Success: false,
			Message: "响应解析失败: " + err.Error(),
		}, nil
	}
	return &result, nil
}

func (c *GnnClient) GetLatencyStats() (*LatencyResponse, error) {
	httpReq, _ := http.NewRequest("GET", c.baseURL+"/v1/stats/latency", nil)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return &LatencyResponse{
			Count: 0,
			P99Ms: 3000,
		}, nil
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	var result LatencyResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return &LatencyResponse{
			Count: 0,
			P99Ms: 3000,
		}, nil
	}
	return &result, nil
}

func (c *GnnClient) HealthCheck() (bool, error) {
	httpReq, _ := http.NewRequest("GET", c.baseURL+"/health", nil)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == 200, nil
}

func (c *GnnClient) fallbackResponse(req PredictSpreadRequest) *PredictSpreadResponse {
	return &PredictSpreadResponse{
		SourceBed:    req.SourceBed,
		BacteriaName: req.BacteriaName,
		SpreadProb:   0.5,
		SpreadPath:   []uint32{req.SourceBed},
		EdgeWeights:  []float64{},
		IsFallback:   true,
		Success:      true,
		Error:        "客户端降级：服务不可达",
	}
}
