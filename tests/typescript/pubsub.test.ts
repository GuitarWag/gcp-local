import { describe, expect, it } from "vitest";

const PROJECT = "local-project";

function base(): string {
  const host = process.env.GCP_LOCAL_HOST;
  if (!host) throw new Error("GCP_LOCAL_HOST not set");
  return `http://${host}/v1/projects/${PROJECT}`;
}

async function json(
  method: string,
  url: string,
  body?: unknown,
): Promise<{ status: number; body: any }> {
  const res = await fetch(url, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let parsed: any = text;
  try {
    parsed = text ? JSON.parse(text) : undefined;
  } catch {
    // keep text
  }
  return { status: res.status, body: parsed };
}

describe("pubsub", () => {
  it("publish, pull, ack roundtrip", async () => {
    const b = base();

    const created = await json("PUT", `${b}/topics/ts-topic`);
    expect(created.status).toBe(200);

    const sub = await json("PUT", `${b}/subscriptions/ts-sub`, {
      topic: `projects/${PROJECT}/topics/ts-topic`,
      ackDeadlineSeconds: 10,
    });
    expect(sub.status).toBe(200);

    const pub = await json("POST", `${b}/topics/ts-topic:publish`, {
      messages: [
        { data: Buffer.from("first").toString("base64") },
        { data: Buffer.from("second").toString("base64") },
      ],
    });
    expect(pub.status).toBe(200);
    expect(pub.body.messageIds).toHaveLength(2);

    const pull = await json("POST", `${b}/subscriptions/ts-sub:pull`, {
      maxMessages: 10,
      returnImmediately: true,
    });
    expect(pull.status).toBe(200);
    const received: any[] = pull.body.receivedMessages;
    expect(received).toHaveLength(2);
    const decoded = received
      .map((m) => Buffer.from(m.message.data, "base64").toString())
      .sort();
    expect(decoded).toEqual(["first", "second"]);

    const ack = await json("POST", `${b}/subscriptions/ts-sub:acknowledge`, {
      ackIds: received.map((m) => m.ackId),
    });
    expect(ack.status).toBe(200);
  });

  it("publish to missing topic returns 404", async () => {
    const res = await json("POST", `${base()}/topics/missing:publish`, {
      messages: [],
    });
    expect(res.status).toBe(404);
  });
});
