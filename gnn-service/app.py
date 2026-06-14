import numpy as np
from fastapi import FastAPI
from pydantic import BaseModel
from typing import List

app = FastAPI(title="GNN Resistance Spread Prediction Service")

np.random.seed(42)

_WEIGHTS_INITIALIZED = False
_W1 = None
_W2 = None
_HIDDEN_DIM = 16


def _ensure_weights(input_dim: int):
    global _WEIGHTS_INITIALIZED, _W1, _W2
    if _WEIGHTS_INITIALIZED:
        return
    rng = np.random.RandomState(42)
    _W1 = rng.randn(input_dim, _HIDDEN_DIM) * 0.01
    _W2 = rng.randn(_HIDDEN_DIM, _HIDDEN_DIM) * 0.01
    _WEIGHTS_INITIALIZED = True


def _normalize_adjacency(A: np.ndarray) -> np.ndarray:
    A_hat = A + np.eye(A.shape[0])
    D = np.diag(np.power(A_hat.sum(axis=1), -0.5))
    D[np.isinf(D)] = 0.0
    return D @ A_hat @ D


def _relu(x: np.ndarray) -> np.ndarray:
    return np.maximum(0, x)


def _sigmoid(x: np.ndarray) -> np.ndarray:
    return 1.0 / (1.0 + np.exp(-np.clip(x, -500, 500)))


class GNNSpreadsRequest(BaseModel):
    source_bed: int
    bacteria: str
    adjacency: List[List[float]]
    node_features: List[List[float]]


class GNNSpreadsResponse(BaseModel):
    spread_prob: float
    path: List[int]
    edge_weights: List[float]


@app.post("/predict/gnn_spread", response_model=GNNSpreadsResponse)
def predict_gnn_spread(req: GNNSpreadsRequest):
    A = np.array(req.adjacency, dtype=np.float64)
    H = np.array(req.node_features, dtype=np.float64)
    N = A.shape[0]

    _ensure_weights(H.shape[1] if H.ndim == 2 else 1)

    A_norm = _normalize_adjacency(A)

    H1 = _relu(A_norm @ H @ _W1)
    H2 = _relu(A_norm @ H1 @ _W2)

    source_idx = int(req.source_bed)
    if source_idx >= N:
        source_idx = N - 1
    if source_idx < 0:
        source_idx = 0

    source_repr = H2[source_idx:source_idx + 1, :]
    scores = (H2 @ source_repr.T).flatten()
    probs = _sigmoid(scores)

    max_prob = float(np.max(probs[np.arange(N) != source_idx])) if N > 1 else 0.0

    candidate_indices = [i for i in range(N) if i != source_idx]
    candidate_probs = [probs[i] for i in candidate_indices]
    ranked = sorted(zip(candidate_indices, candidate_probs), key=lambda x: x[1], reverse=True)

    top_k = min(5, len(ranked))
    top_nodes = ranked[:top_k]

    path = [source_idx] + [idx for idx, _ in top_nodes]

    edge_weights = []
    for i in range(len(path) - 1):
        u, v = path[i], path[i + 1]
        edge_weights.append(float(A[u, v]))

    return GNNSpreadsResponse(
        spread_prob=float(max_prob),
        path=path,
        edge_weights=edge_weights,
    )


@app.get("/health")
def health():
    return {"status": "ok"}


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8000)
