#!/usr/bin/env python3
"""Preload unchanged CI image names from verified immutable OCI sources.

The OCI index digests below were checked against the matching Docker Hub tags.
Postgres/Redis are Docker Official Images via Google's Docker Hub cache; Ryuk
is published by Testcontainers on GHCR. Docker verifies immutable digests;
existing tests use these unchanged local aliases and no credentials are rotated.
"""

import subprocess


IMAGES = (
    ("mirror.gcr.io/library/postgres@sha256:aa6eb304ddb6dd26df23d05db4e5cb05af8951cda3e0dc57731b771e0ef4ab29", "postgres:18.1-alpine3.23"),
    ("mirror.gcr.io/library/redis@sha256:0514fa59e3d84e2f3be66731c5401fc2a85b8ea71b49c0116c850f6a558076f4", "redis:8.4-alpine"),
    ("ghcr.io/testcontainers/ryuk@sha256:31b31269d06603366cbfd0284708dcd2e281e8a4188e53fce3d3304439d0df3d", "testcontainers/ryuk:0.13.0"),
)


def prepare(run=subprocess.run):
    for source, local_name in IMAGES:
        run(["docker", "pull", "--platform", "linux/amd64", source], check=True)
        run(["docker", "tag", source, local_name], check=True)


if __name__ == "__main__":
    prepare()
