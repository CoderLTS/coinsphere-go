# syntax=docker/dockerfile:1
FROM ghcr.io/astral-sh/uv:0.11.1@sha256:fc93e9ecd7218e9ec8fba117af89348eef8fd2463c50c13347478769aaedd0ce AS uv

FROM python:3.12-slim-bookworm@sha256:d50fb7611f86d04a3b0471b46d7557818d88983fc3136726336b2a4c657aa30b
ENV PATH="/app/.venv/bin:${PATH}" \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1
WORKDIR /app
COPY --from=uv /uv /bin/uv
COPY pyproject.toml uv.lock ./
RUN --mount=type=cache,id=coinsphere-uv,target=/root/.cache/uv \
    UV_LINK_MODE=copy uv sync --locked --no-dev --no-install-project
COPY coinsphere_worker ./coinsphere_worker
RUN useradd --create-home --uid 10001 worker \
    && chown -R worker:worker /app
USER worker
HEALTHCHECK --interval=10s --timeout=3s --start-period=3s --retries=3 \
    CMD ["python", "-m", "coinsphere_worker", "health"]
CMD ["python", "-m", "coinsphere_worker", "run"]
