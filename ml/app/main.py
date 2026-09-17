"""Verified Property Score sidecar (spec §13). Never exposed publicly —
reached only server-to-server from internal/scoring."""

from fastapi import Depends, FastAPI, HTTPException
from pydantic import BaseModel

from app.core.model_loader import load_model
from app.dependencies import require_shared_secret
from app.services.scoring_pipeline import UnknownPropertyError, score_property

app = FastAPI(title="willcoll-ml-sidecar")


@app.on_event("startup")
def _load_model_once() -> None:
    load_model()


class ScoreRequest(BaseModel):
    requested_by: str
    operating_expenses: float
    annual_debt_service: float
    other_income: float = 0


@app.post("/v1/score/{property_id}", dependencies=[Depends(require_shared_secret)])
def request_score(property_id: str, body: ScoreRequest):
    try:
        return score_property(
            property_id=property_id,
            requested_by=body.requested_by,
            operating_expenses=body.operating_expenses,
            annual_debt_service=body.annual_debt_service,
            other_income=body.other_income,
        )
    except UnknownPropertyError as exc:
        raise HTTPException(status_code=404, detail=str(exc)) from exc
    except ValueError as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
