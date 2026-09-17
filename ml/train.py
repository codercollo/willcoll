"""Offline training script (spec §13.1/§13.5) — never imported or called by
the live API; run manually: `python train.py`. Trains a small GBDT on
prepared/synthetic historical data and saves ml/model.joblib.
"""

from pathlib import Path

import joblib
import numpy as np
from sklearn.ensemble import GradientBoostingRegressor

MODEL_VERSION = "v1"
FEATURES = ["collection_rate", "vacancy_periods", "arrears_recovery_days", "dscr"]
MODEL_PATH = Path(__file__).resolve().parent / "model.joblib"


def _synthetic_training_data(n: int = 200, seed: int = 0):
    rng = np.random.default_rng(seed)
    collection_rate = rng.uniform(0.3, 1.0, n)
    vacancy_periods = rng.integers(0, 6, n)
    arrears_recovery_days = rng.uniform(1, 60, n)
    dscr = rng.uniform(0.5, 2.5, n)
    X = np.column_stack([collection_rate, vacancy_periods, arrears_recovery_days, dscr])
    y = np.clip(
        collection_rate * 60 - vacancy_periods * 3 - arrears_recovery_days * 0.2 + dscr * 10,
        0,
        100,
    )
    return X, y


def main() -> None:
    X, y = _synthetic_training_data()
    model = GradientBoostingRegressor(n_estimators=50, max_depth=2, random_state=0)
    model.fit(X, y)
    joblib.dump({"model": model, "version": MODEL_VERSION, "features": FEATURES}, MODEL_PATH)
    print(f"saved {MODEL_PATH} ({MODEL_VERSION}, {MODEL_PATH.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
