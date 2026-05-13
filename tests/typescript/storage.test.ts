import { Storage } from "@google-cloud/storage";
import { beforeAll, describe, expect, it } from "vitest";

function newClient(): Storage {
  const host = process.env.GCP_LOCAL_HOST;
  if (!host) throw new Error("GCP_LOCAL_HOST not set");
  process.env.STORAGE_EMULATOR_HOST = `http://${host}`;
  return new Storage({
    projectId: "local-project",
    apiEndpoint: `http://${host}`,
  });
}

describe("storage", () => {
  let storage: Storage;

  beforeAll(() => {
    storage = newClient();
  });

  it("creates a bucket, uploads, downloads, lists and deletes", async () => {
    const [bucket] = await storage.createBucket("ts-bucket");
    const file = bucket.file("greeting.txt");
    await file.save("hello from typescript", { contentType: "text/plain" });

    const [data] = await file.download();
    expect(data.toString()).toBe("hello from typescript");

    const [files] = await bucket.getFiles();
    expect(files.map((f) => f.name)).toEqual(["greeting.txt"]);

    await file.delete();
    await bucket.delete();
  });

  it("returns 404 for a missing object", async () => {
    const [bucket] = await storage.createBucket("ts-bucket-2");
    try {
      await expect(bucket.file("nope").download()).rejects.toThrow();
    } finally {
      await bucket.delete();
    }
  });
});
