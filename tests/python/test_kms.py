import base64
import requests


PROJECT = "local-project"


def test_kms_encrypt_decrypt_roundtrip(emulator_url):
    base = f"{emulator_url}/v1/projects/{PROJECT}/locations/us/keyRings"

    r = requests.post(f"{base}?keyRingId=py-ring")
    assert r.status_code == 200, r.text

    r = requests.post(
        f"{base}/py-ring/cryptoKeys?cryptoKeyId=py-key",
        json={"purpose": "ENCRYPT_DECRYPT"},
    )
    assert r.status_code == 200

    plain = base64.b64encode(b"top secret py").decode()
    r = requests.post(f"{base}/py-ring/cryptoKeys/py-key:encrypt", json={"plaintext": plain})
    assert r.status_code == 200
    ct = r.json()["ciphertext"]

    r = requests.post(f"{base}/py-ring/cryptoKeys/py-key:decrypt", json={"ciphertext": ct})
    assert r.status_code == 200
    assert base64.b64decode(r.json()["plaintext"]) == b"top secret py"


def test_kms_tampered_ciphertext_rejected(emulator_url):
    base = f"{emulator_url}/v1/projects/{PROJECT}/locations/us/keyRings"
    requests.post(f"{base}?keyRingId=py-tamper")
    requests.post(f"{base}/py-tamper/cryptoKeys?cryptoKeyId=k", json={})
    r = requests.post(
        f"{base}/py-tamper/cryptoKeys/k:encrypt",
        json={"plaintext": base64.b64encode(b"x").decode()},
    )
    ct = base64.b64decode(r.json()["ciphertext"])
    tampered = base64.b64encode(ct[:-1] + bytes([ct[-1] ^ 0xFF])).decode()
    r = requests.post(
        f"{base}/py-tamper/cryptoKeys/k:decrypt",
        json={"ciphertext": tampered},
    )
    assert r.status_code != 200
