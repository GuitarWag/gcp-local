import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { Firestore } from "@google-cloud/firestore";
import { PubSub } from "@google-cloud/pubsub";

function host(): string {
  const h = process.env.GCP_LOCAL_HOST;
  if (!h) throw new Error("GCP_LOCAL_HOST not set");
  return h;
}

describe("firestore SDK (gRPC)", () => {
  let fs: Firestore;

  beforeAll(() => {
    process.env.FIRESTORE_EMULATOR_HOST = host();
    fs = new Firestore({ projectId: "local-project" });
  });

  afterAll(async () => {
    await fs.terminate();
  });

  it("set / get / delete document", async () => {
    const doc = fs.collection("ts-coll").doc("alice");
    await doc.set({ name: "Alice", age: 30 });
    const snap = await doc.get();
    const data = snap.data() ?? {};
    expect(data.name).toBe("Alice");
    expect(data.age).toBe(30);
    await doc.delete();
  });
});

describe("pubsub SDK (gRPC)", () => {
  let ps: PubSub;

  beforeAll(() => {
    process.env.PUBSUB_EMULATOR_HOST = host();
    process.env.PUBSUB_PROJECT_ID = "local-project";
    ps = new PubSub({ projectId: "local-project" });
  });

  afterAll(async () => {
    await ps.close();
  });

  it("publish and pull via SDK", async () => {
    const [topic] = await ps.createTopic("ts-grpc-topic");
    const [sub] = await topic.createSubscription("ts-grpc-sub");

    await topic.publishMessage({ data: Buffer.from("hello-ts-grpc") });

    const received: string[] = await new Promise((resolve) => {
      const out: string[] = [];
      const timer = setTimeout(() => {
        sub.removeAllListeners();
        resolve(out);
      }, 4000);
      sub.on("message", (m) => {
        out.push(m.data.toString());
        m.ack();
        if (out.length >= 1) {
          clearTimeout(timer);
          sub.removeAllListeners();
          resolve(out);
        }
      });
      sub.on("error", () => {
        clearTimeout(timer);
        sub.removeAllListeners();
        resolve(out);
      });
    });

    expect(received).toContain("hello-ts-grpc");
  }, 10000);
});
