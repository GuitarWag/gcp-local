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

describe("secretmanager", () => {
  it("creates, adds version, accesses, deletes", async () => {
    const base = `http://${host()}/v1/projects/${PROJECT}/secrets`;
    let r = await j("POST", `${base}?secretId=ts-secret`, {});
    expect(r.status).toBe(200);

    const payload = Buffer.from("ts-classified").toString("base64");
    r = await j("POST", `${base}/ts-secret:addVersion`, { payload: { data: payload } });
    expect(r.status).toBe(200);

    r = await j("GET", `${base}/ts-secret/versions/latest:access`);
    expect(r.status).toBe(200);
    const decoded = Buffer.from(r.body.payload.data, "base64").toString();
    expect(decoded).toBe("ts-classified");

    r = await j("DELETE", `${base}/ts-secret`);
    expect(r.status).toBe(204);
  });

  it("missing secretId returns 400", async () => {
    const r = await j("POST", `http://${host()}/v1/projects/${PROJECT}/secrets`, {});
    expect(r.status).toBe(400);
  });
});
