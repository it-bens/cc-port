# Dev S3 backend

A single-node [Garage](https://garagehq.deuxfleurs.fr/) container that exposes an S3-compatible API on `http://localhost:9000`. Used by the demo videos under `docs/videos/` and (planned) E2E tests against `internal/sync` / `internal/remote`.

## Why Garage

MinIO is archived and the Community Edition has been hollowed out over 2025. LocalStack follows the same community/Pro pattern. Adobe S3Mock is a mock, and cc-port's `push --force` and cross-machine conflict refusal lean on real `If-Match` and metadata semantics, especially once the backend is reused for E2E. SeaweedFS is the documented fallback (Apache 2.0, larger production track record).

Garage wins on community signal (community-led by Deuxfleurs, no enterprise tier), single Rust binary, multi-arch. The AGPL-3.0 license is a non-issue because cc-port consumes Garage as a service, not redistributed.

## Lifecycle

From the repository root:

```
make s3-up      # Start Garage and wait for the S3 API to respond
make s3-down    # Stop Garage; preserves data volumes
make s3-reset   # Destroy data volumes and start fresh
```

## Endpoint and credentials

| Setting | Value |
|---|---|
| S3 API endpoint | `http://localhost:9000` |
| Region | `us-east-1` |
| Bucket (auto-created on first start) | `cc-port-dev` |
| Access key | sourced from `dev/s3/env` (`GARAGE_DEFAULT_ACCESS_KEY`) |
| Secret key | sourced from `dev/s3/env` (`GARAGE_DEFAULT_SECRET_KEY`) |

The credentials in `dev/s3/env` are deterministic zeros, plainly fake, and clearly marked test-only. They exist so that the demo tapes and integration tests produce identical fixtures. **Never use them in production.**

## Container runtime

Docker Compose v2 is the documented default. Podman with `podman compose` is expected to work because the compose file uses no Docker-specific extensions, but is not actively maintained.

## Auto-bootstrap

Garage v2.3.0's `garage server --single-node --default-bucket` reads `GARAGE_DEFAULT_ACCESS_KEY`, `GARAGE_DEFAULT_SECRET_KEY`, and `GARAGE_DEFAULT_BUCKET` from the environment on first start and provisions the cluster layout, access key, bucket, and bucket policy in one shot. No separate init script is needed.

## Smoke test

Once `make s3-up` completes:

```bash
source dev/s3/env
AWS_ACCESS_KEY_ID=$GARAGE_DEFAULT_ACCESS_KEY \
AWS_SECRET_ACCESS_KEY=$GARAGE_DEFAULT_SECRET_KEY \
aws --endpoint-url http://localhost:9000 --region us-east-1 s3 ls
```

should list `cc-port-dev`.
