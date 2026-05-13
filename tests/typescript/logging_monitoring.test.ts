import { describe, expect, it } from "vitest";

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

describe("logging + monitoring", () => {
  it("logging: write and list entries", async () => {
    const base = `http://${host()}/v2/entries`;
    expect((await j("POST", `${base}:write`, {
      logName: `projects/${PROJECT}/logs/ts`,
      resource: { type: "global" },
      entries: [{ severity: "INFO", textPayload: "ts-1" }],
    })).status).toBe(200);
    const r = await j("POST", `${base}:list`, { resourceNames: [`projects/${PROJECT}`] });
    expect(r.status).toBe(200);
    const entries: any[] = r.body.entries || [];
    expect(entries.some((e) => e.textPayload === "ts-1")).toBe(true);
  });

  it("monitoring: create + list timeseries", async () => {
    const base = `http://${host()}/v3/projects/${PROJECT}/timeSeries`;
    expect((await j("POST", base, {
      timeSeries: [
        {
          metric: { type: "custom.googleapis.com/ts" },
          resource: { type: "global" },
          points: [{ interval: { endTime: "2026-01-01T00:00:00Z" }, value: { doubleValue: 1.0 } }],
        },
      ],
    })).status).toBe(200);
    const r = await j("GET", base);
    expect(r.status).toBe(200);
    expect((r.body.timeSeries || []).length).toBeGreaterThanOrEqual(1);
  });
});
