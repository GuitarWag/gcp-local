import base64

import requests


PROJECT = "local-project"


def test_pubsub_publish_pull_ack(emulator_url):
    base = f"{emulator_url}/v1/projects/{PROJECT}"

    r = requests.put(f"{base}/topics/py-topic")
    assert r.status_code == 200, r.text

    r = requests.put(
        f"{base}/subscriptions/py-sub",
        json={
            "topic": f"projects/{PROJECT}/topics/py-topic",
            "ackDeadlineSeconds": 10,
        },
    )
    assert r.status_code == 200, r.text

    r = requests.post(
        f"{base}/topics/py-topic:publish",
        json={
            "messages": [
                {"data": base64.b64encode(b"hello").decode()},
                {"data": base64.b64encode(b"world").decode()},
            ]
        },
    )
    assert r.status_code == 200, r.text
    ids = r.json()["messageIds"]
    assert len(ids) == 2

    r = requests.post(
        f"{base}/subscriptions/py-sub:pull",
        json={"maxMessages": 10, "returnImmediately": True},
    )
    assert r.status_code == 200, r.text
    received = r.json()["receivedMessages"]
    assert len(received) == 2
    payloads = sorted(base64.b64decode(m["message"]["data"]) for m in received)
    assert payloads == [b"hello", b"world"]

    ack_ids = [m["ackId"] for m in received]
    r = requests.post(
        f"{base}/subscriptions/py-sub:acknowledge",
        json={"ackIds": ack_ids},
    )
    assert r.status_code == 200, r.text


def test_pubsub_publish_to_missing_topic_is_404(emulator_url):
    r = requests.post(
        f"{emulator_url}/v1/projects/{PROJECT}/topics/missing:publish",
        json={"messages": []},
    )
    assert r.status_code == 404
