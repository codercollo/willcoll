# Model card — Verified Property Score

- **version**: `v1`
- **type**: Gradient-boosted decision tree regressor (`sklearn.ensemble.GradientBoostingRegressor`), 50 estimators, max_depth 2
- **trained**: offline via `train.py`, never at request time (spec §13.1/§13.5)
- **size**: ~48 KB (sub-MB, CPU-only)
- **features** (order matters, see `train.py:FEATURES`): `collection_rate`, `vacancy_periods`, `arrears_recovery_days`, `dscr`
- **output**: `score_value` (0–100), mapped to `score_band` A/B/C/D
- **training data**: synthetic placeholder (`_synthetic_training_data`) — replace with prepared/anonymized historical data before production use; retrain by re-running `train.py` and bumping `MODEL_VERSION`.
