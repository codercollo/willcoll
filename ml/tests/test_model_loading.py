from pathlib import Path

import pytest

from app.core import model_loader

MODEL_PATH = Path(__file__).resolve().parent.parent / "model.joblib"


def test_loads_real_model_file():
    model_loader.load_model(MODEL_PATH)
    model, version, features = model_loader.get_model()
    assert model is not None
    assert version
    assert features


def test_missing_model_fails_loudly(tmp_path):
    with pytest.raises(model_loader.ModelNotLoadedError):
        model_loader.load_model(tmp_path / "does-not-exist.joblib")


def test_corrupt_model_fails_loudly(tmp_path):
    bad = tmp_path / "corrupt.joblib"
    bad.write_bytes(b"not a joblib file")
    with pytest.raises(model_loader.ModelNotLoadedError):
        model_loader.load_model(bad)


def test_get_model_before_load_fails_loudly():
    # Reset module-level state to simulate a fresh process that never
    # called load_model() at startup.
    model_loader._model = None
    with pytest.raises(model_loader.ModelNotLoadedError):
        model_loader.get_model()
