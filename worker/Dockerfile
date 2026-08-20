# syntax=docker/dockerfile:1
FROM ghcr.io/astral-sh/uv:0.11.1@sha256:b6ca5767729b57719f1edf206123c8cb474a4b3a198cc28e1fd92eee3d3898fe AS uv

FROM python:3.12-alpine@sha256:6d43704baacd1bfbe7c295d7f13079d5d8104ed33568873133f8fc69980419df
ENV PATH="/app/.venv/bin:${PATH}" \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1
WORKDIR /app
COPY --from=uv /uv /bin/uv
COPY pyproject.toml uv.lock ./
RUN --mount=type=cache,id=coinsphere-uv,target=/root/.cache/uv \
    UV_LINK_MODE=copy uv sync --locked --no-dev --no-install-project
COPY coinsphere_worker ./coinsphere_worker
RUN adduser -D -H -u 10001 worker \
    && mkdir -p /app/artifacts \
    && chown -R worker:worker /app
USER worker
HEALTHCHECK --interval=10s --timeout=3s --start-period=3s --retries=3 \
    CMD ["python", "-m", "coinsphere_worker", "health"]
CMD ["python", "-m", "coinsphere_worker", "run"]
