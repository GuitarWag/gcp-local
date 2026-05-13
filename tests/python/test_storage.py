from google.cloud.exceptions import NotFound


def test_storage_bucket_and_object_roundtrip(storage_client):
    bucket = storage_client.bucket("py-bucket")
    bucket.create()

    blob = bucket.blob("greeting.txt")
    blob.upload_from_string("hello from python", content_type="text/plain")

    fetched = bucket.blob("greeting.txt").download_as_bytes()
    assert fetched == b"hello from python"

    names = [b.name for b in storage_client.list_blobs("py-bucket")]
    assert names == ["greeting.txt"]

    bucket.blob("greeting.txt").delete()
    bucket.delete()


def test_storage_missing_object_raises_not_found(storage_client):
    bucket = storage_client.bucket("py-bucket-2")
    bucket.create()
    try:
        try:
            bucket.blob("nope").download_as_bytes()
            assert False, "expected NotFound"
        except NotFound:
            pass
    finally:
        bucket.delete()
