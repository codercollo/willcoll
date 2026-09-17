"""Loads model.joblib once at process startup, never per-request (spec
§13.1). Fails loudly if the file is missing/corrupt — a scoring request
should never silently fall back to no model."""

from pathlib import Path

import joblib

_MODEL_PATH = Path(__file__).resolve().parent.parent.parent / "model.joblib"

_model = None
_model_version = None
_features: list[str] = []


class ModelNotLoadedError(RuntimeError):
    pass


def load_model(path: Path = _MODEL_PATH) -> None:
    global _model, _model_version, _features
    if not path.exists():
        raise ModelNotLoadedError(f"model file not found: {path}")
    try:
        bundle = joblib.load(path)
    except Exception as exc:  # noqa: BLE001 - any load failure must be loud
        raise ModelNotLoadedError(f"failed to load model at {path}: {exc}") from exc

    for key in ("model", "version", "features"):
        if key not in bundle:
            raise ModelNotLoadedError(f"model bundle at {path} missing {key!r}")

    _model, _model_version, _features = bundle["model"], bundle["version"], bundle["features"]


def get_model():
    """Returns (model, version, feature_order). Raises if load_model() was
    never called or failed — never returns a partially-loaded state."""
    if _model is None:
        raise ModelNotLoadedError("model not loaded — call load_model() at startup")
    return _model, _model_version, _features
