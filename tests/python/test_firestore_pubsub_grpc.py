import os
import time

import pytest


@pytest.fixture
def firestore_emulator_env(emulator):
    os.environ["FIRESTORE_EMULATOR_HOST"] = emulator["host"]
    yield emulator
    os.environ.pop("FIRESTORE_EMULATOR_HOST", None)


def test_firestore_set_get_via_sdk(firestore_emulator_env):
    from google.cloud import firestore

    client = firestore.Client(project="local-project")
    doc = client.collection("py-coll").document("alice")
    doc.set({"name": "Alice", "age": 30, "tags": ["a", "b"]})
    snap = doc.get()
    data = snap.to_dict()
    assert data["name"] == "Alice"
    assert data["age"] == 30
    assert data["tags"] == ["a", "b"]
    doc.delete()


@pytest.fixture
def pubsub_emulator_env(emulator):
    os.environ["PUBSUB_EMULATOR_HOST"] = emulator["host"]
    os.environ["PUBSUB_PROJECT_ID"] = "local-project"
    yield emulator
    os.environ.pop("PUBSUB_EMULATOR_HOST", None)


def test_pubsub_publish_pull_via_sdk(pubsub_emulator_env):
    from google.cloud import pubsub_v1

    publisher = pubsub_v1.PublisherClient()
    subscriber = pubsub_v1.SubscriberClient()
    topic = publisher.topic_path("local-project", "py-grpc-topic")
    sub = subscriber.subscription_path("local-project", "py-grpc-sub")
    publisher.create_topic(request={"name": topic})
    subscriber.create_subscription(request={"name": sub, "topic": topic})

    publisher.publish(topic, b"hello-grpc").result(timeout=5)

    response = subscriber.pull(request={"subscription": sub, "max_messages": 10}, timeout=5)
    assert any(m.message.data == b"hello-grpc" for m in response.received_messages)
    if response.received_messages:
        subscriber.acknowledge(
            request={"subscription": sub, "ack_ids": [m.ack_id for m in response.received_messages]}
        )
