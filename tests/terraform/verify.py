"""Verifier for the Terraform harness.

Usage: verify.py present|absent

Endpoint: env var GCP_LOCAL_HOST (host:port of gcp-local, e.g.
localhost:18085). Project: test-project.

Run with the tests/python venv:
    .venv/bin/python tests/terraform/verify.py present

present mode asserts that terraform apply created every resource.
absent mode asserts that terraform destroy removed every resource and
that the emulator's 404s keep the expected error shape.
"""

import json
import os
import sys

import requests
from google.api_core.exceptions import NotFound
from google.auth.credentials import AnonymousCredentials
from google.cloud import pubsub_v1, secretmanager_v1, storage
from google.cloud.secretmanager_v1.services.secret_manager_service.transports import (
    SecretManagerServiceRestTransport,
)

PROJECT = "test-project"
BUCKET = "tf-accept-bucket"
TOPIC = "tf-accept-topic"
SUBSCRIPTION = "tf-accept-sub"
SECRET = "tf-accept-secret"
PUSH_ENDPOINT = "https://example.internal/hook"
ACK_DEADLINE = 10
CALL_TIMEOUT = 30

USAGE = "usage: verify.py present|absent"

failures = []


def ok(resource, detail):
    print(f"ok {resource}: {detail}")


def fail(resource, detail):
    line = f"FAIL {resource}: {detail}"
    print(line, file=sys.stderr)
    failures.append(line)


def note(resource, detail):
    print(f"note {resource}: {detail}")


def error_body(exc):
    """Parse the HTTP body behind a REST NotFound, or None for gRPC."""
    response = getattr(exc, "_response", None)
    if response is None:
        return None
    try:
        return json.loads(response.text)
    except ValueError:
        return None


def check_rest_absent(resource, exc, missing_field, missing_real):
    """Shared absent-mode checks for the REST-based services.

    The emulator returns a bare {"error": {"code", "message"}} body. The
    real APIs also carry the named detail field, so report its absence as
    a divergence instead of failing.
    """
    body = error_body(exc)
    if exc.code != 404:
        fail(resource, f"expected 404, got {exc.code}")
        return
    if not isinstance(body, dict) or not isinstance(body.get("error"), dict):
        fail(resource, f"expected error object in body, got {body!r}")
        return
    err = body["error"]
    if err.get("code") != 404 or not err.get("message"):
        fail(resource, f"expected error.code=404 and error.message, got {err!r}")
        return
    if missing_field not in err:
        note(resource, f"404 body has no {missing_field} field "
                       f"(real API carries {missing_real}); divergence, follow-up")
    ok(resource, "absent with 404 error body {code,message}")


def check_grpc_absent(resource, exc):
    if getattr(exc.grpc_status_code, "name", None) != "NOT_FOUND":
        fail(resource, f"expected gRPC status NOT_FOUND, got {exc.grpc_status_code}")
        return
    ok(resource, "absent with gRPC status NOT_FOUND")


def check_bucket(client, mode):
    resource = f"storage bucket {BUCKET}"
    try:
        bucket = client.get_bucket(BUCKET)
    except NotFound as exc:
        if mode == "absent":
            check_rest_absent(resource, exc, "reason", "reason: notFound")
        else:
            fail(resource, f"not found: {exc}")
        return
    except Exception as exc:
        fail(resource, f"unexpected error: {exc}")
        return
    if mode == "absent":
        fail(resource, "still present after destroy")
        return
    problems = []
    if (bucket.location or "").upper() != "US":
        problems.append(f"location {bucket.location!r}")
    if (bucket.storage_class or "").upper() != "STANDARD":
        problems.append(f"storage_class {bucket.storage_class!r}")
    if problems:
        fail(resource, "; ".join(problems))
        return
    ok(resource, f"location={bucket.location} storage_class={bucket.storage_class}")


def check_topic(publisher, mode):
    resource = f"pubsub topic {TOPIC}"
    path = f"projects/{PROJECT}/topics/{TOPIC}"
    try:
        topic = publisher.get_topic(topic=path, timeout=CALL_TIMEOUT)
    except NotFound as exc:
        if mode == "absent":
            check_grpc_absent(resource, exc)
        else:
            fail(resource, f"not found: {exc}")
        return
    except Exception as exc:
        fail(resource, f"unexpected error: {exc}")
        return
    if mode == "absent":
        fail(resource, "still present after destroy")
        return
    if topic.name != path:
        fail(resource, f"expected name {path}, got {topic.name!r}")
        return
    ok(resource, f"name={topic.name}")


def check_subscription(subscriber, host, mode):
    resource = f"pubsub subscription {SUBSCRIPTION}"
    path = f"projects/{PROJECT}/subscriptions/{SUBSCRIPTION}"
    topic_path = f"projects/{PROJECT}/topics/{TOPIC}"
    try:
        sub = subscriber.get_subscription(subscription=path, timeout=CALL_TIMEOUT)
    except NotFound as exc:
        if mode == "absent":
            check_grpc_absent(resource, exc)
        else:
            fail(resource, f"not found: {exc}")
        return
    except Exception as exc:
        fail(resource, f"unexpected error: {exc}")
        return
    if mode == "absent":
        fail(resource, "still present after destroy")
        return
    problems = []
    if sub.topic != topic_path:
        problems.append(f"topic {sub.topic!r}")
    if sub.ack_deadline_seconds != ACK_DEADLINE:
        problems.append(f"ack_deadline_seconds {sub.ack_deadline_seconds!r}")
    if sub.push_config.push_endpoint != PUSH_ENDPOINT:
        # The emulator's gRPC GetSubscription drops PushConfig; the REST
        # body keeps it. Check the REST wire, which is what the provider
        # used, and note the gRPC gap.
        rest = requests.get(f"http://{host}/v1/{path}", timeout=CALL_TIMEOUT)
        if rest.status_code != 200:
            problems.append(f"REST GET returned {rest.status_code}")
        else:
            endpoint = rest.json().get("pushConfig", {}).get("pushEndpoint", "")
            if endpoint != PUSH_ENDPOINT:
                problems.append(f"push_endpoint {endpoint!r}")
            elif not problems:
                note(resource, "gRPC GetSubscription returns no pushConfig; "
                               "push endpoint verified over REST; divergence, follow-up")
    if problems:
        fail(resource, "; ".join(problems))
        return
    ok(resource, f"topic={sub.topic} push_endpoint={PUSH_ENDPOINT} "
                 f"ack_deadline={sub.ack_deadline_seconds}")


def check_secret(client, mode):
    resource = f"secret manager secret {SECRET}"
    path = f"projects/{PROJECT}/secrets/{SECRET}"
    try:
        secret = client.get_secret(name=path, timeout=CALL_TIMEOUT)
    except NotFound as exc:
        if mode == "absent":
            check_rest_absent(resource, exc, "status", "status: NOT_FOUND")
        else:
            fail(resource, f"not found: {exc}")
        return
    except Exception as exc:
        fail(resource, f"unexpected error: {exc}")
        return
    if mode == "absent":
        fail(resource, "still present after destroy")
        return
    if secret.name != path:
        fail(resource, f"expected name {path}, got {secret.name!r}")
        return
    ok(resource, f"name={secret.name}")


def build_clients(host):
    # The pubsub gRPC transport only dials plain HTTP through the
    # emulator env var; a client_options endpoint makes it try TLS.
    os.environ["PUBSUB_EMULATOR_HOST"] = host
    storage_client = storage.Client(
        project=PROJECT,
        credentials=AnonymousCredentials(),
        client_options={"api_endpoint": f"http://{host}"},
    )
    publisher = pubsub_v1.PublisherClient(credentials=AnonymousCredentials())
    subscriber = pubsub_v1.SubscriberClient(credentials=AnonymousCredentials())
    secret_client = secretmanager_v1.SecretManagerServiceClient(
        credentials=AnonymousCredentials(),
        transport=SecretManagerServiceRestTransport,
        client_options={"api_endpoint": f"http://{host}"},
    )
    return storage_client, publisher, subscriber, secret_client


def main(argv):
    if len(argv) != 2 or argv[1] not in ("present", "absent"):
        print(USAGE, file=sys.stderr)
        return 2
    host = os.environ.get("GCP_LOCAL_HOST")
    if not host:
        print("GCP_LOCAL_HOST is not set (e.g. localhost:18085)", file=sys.stderr)
        return 2
    mode = argv[1]
    storage_client, publisher, subscriber, secret_client = build_clients(host)
    check_bucket(storage_client, mode)
    check_topic(publisher, mode)
    check_subscription(subscriber, host, mode)
    check_secret(secret_client, mode)
    if failures:
        print(f"{len(failures)} check(s) failed", file=sys.stderr)
        return 1
    print(f"all {mode} checks passed")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
