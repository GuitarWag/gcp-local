import { createServer, type Server } from "node:http";
import { setTimeout as delay } from "node:timers/promises";
import { afterEach, beforeEach, describe, expect, it } from "vitest";

const PROJECT = "local-project";

function host(): string {
  const h = process.env.GCP_LOCAL_HOST;
  if (!h) throw new Error("GCP_LOCAL_HOST not set");
  return h;
}

async function j(method: string, url: string, body?: unknown) {
  const res = await fetch(url, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  return { status: res.status, body: text ? JSON.parse(text) : undefined };
}

describe("cloud tasks + scheduler", () => {
  let received: string[] = [];
  let srv: Server;
  let port = 0;

  beforeEach(async () => {
    received = [];
    srv = createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on("data", (c) => chunks.push(c));
      req.on("end", () => {
        received.push(Buffer.concat(chunks).toString());
        res.writeHead(200);
        res.end();
      });
    });
    await new Promise<void>((r) => srv.listen(0, "127.0.0.1", () => r()));
    const addr = srv.address();
    if (addr && typeof addr !== "string") port = addr.port;
  });

  afterEach(() => srv.close());

  it("tasks: queue + task dispatched via HTTP", async () => {
    const base = `http://${host()}/v2/projects/${PROJECT}/locations/loc/queues`;
    expect((await j("POST", base, { name: "ts-q" })).status).toBe(200);
    expect((await j("POST", `${base}/ts-q/tasks`, {
      task: {
        httpRequest: {
          url: `http://127.0.0.1:${port}/`,
          httpMethod: "POST",
          body: Buffer.from("ts-ping").toString("base64"),
        },
      },
    })).status).toBe(200);
    for (let i = 0; i < 100 && received.length === 0; i++) await delay(20);
    expect(received).toEqual(["ts-ping"]);
  });

  it("scheduler: cron job fires repeatedly", async () => {
    const base = `http://${host()}/v1/projects/${PROJECT}/locations/loc/jobs`;
    expect((await j("POST", base, {
      name: "ts-job",
      schedule: "every 100ms",
      httpTarget: { uri: `http://127.0.0.1:${port}/`, httpMethod: "POST" },
    })).status).toBe(200);
    for (let i = 0; i < 100 && received.length < 2; i++) await delay(50);
    expect(received.length).toBeGreaterThanOrEqual(2);
  });
});
