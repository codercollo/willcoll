# Non-root, CPU-only base image — no CUDA/GPU layer; the model itself is
# sub-MB (spec §13.1). dev (uvicorn --reload) -> prod. Never exposed on a
# host port in Compose (server-to-server only, spec §13.3) — EXPOSE here is
# documentation for the internal Compose network, not a published port.

FROM python:3.12-slim-bookworm AS base
WORKDIR /app
COPY pyproject.toml ./
RUN pip install --no-cache-dir .
RUN useradd -u 10001 -m sidecar
COPY . .
RUN chown -R sidecar /app
USER sidecar
EXPOSE 8000

FROM base AS dev
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000", "--reload"]

FROM base AS prod
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
