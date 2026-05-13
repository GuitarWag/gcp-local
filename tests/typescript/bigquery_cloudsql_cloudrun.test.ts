import { createServer, type Server } from "node:http";
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

describe("bigquery", () => {
  it("dataset + table + insert + query roundtrip", async () => {
    const base = `http://${host()}/bigquery/v2/projects/${PROJECT}`;
    expect((await j("POST", `${base}/datasets`, {
      datasetReference: { datasetId: "tsds" },
    })).status).toBe(200);
    expect((await j("POST", `${base}/datasets/tsds/tables`, {
      tableReference: { tableId: "tst" },
      schema: {
        fields: [
          { name: "id", type: "INTEGER" },
          { name: "label", type: "STRING" },
        ],
      },
    })).status).toBe(200);
    expect((await j("POST", `${base}/datasets/tsds/tables/tst/insertAll`, {
      rows: [
        { json: { id: 1, label: "alpha" } },
        { json: { id: 2, label: "beta" } },
      ],
    })).status).toBe(200);
    const r = await j("POST", `${base}/queries`, {
      query: "SELECT label FROM tsds.tst ORDER BY id",
    });
    expect(r.status).toBe(200);
    const values = r.body.rows.flatMap((rr: any) => rr.f.map((c: any) => c.v));
    expect(values).toEqual(["alpha", "beta"]);
  });
});

describe("cloudsql", () => {
  it("instance crud", async () => {
    const base = `http://${host()}/sql/v1beta4/projects/${PROJECT}/instances`;
    expect((await j("POST", base, { name: "ts-sql" })).status).toBe(200);
    const r = await j("GET", `${base}/ts-sql`);
    expect(r.status).toBe(200);
    expect(r.body.state).toBe("RUNNABLE");
    expect((await j("DELETE", `${base}/ts-sql`)).status).toBe(200);
  });
});

describe("cloudrun", () => {
  let srv: Server;
  let port = 0;

  beforeEach(async () => {
    srv = createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on("data", (c) => chunks.push(c));
      req.on("end", () => {
        res.writeHead(200, { "Content-Type": "application/json" });
        res.end(Buffer.concat(chunks));
      });
    });
    await new Promise<void>((r) => srv.listen(0, "127.0.0.1", () => r()));
    const addr = srv.address();
    if (addr && typeof addr !== "string") port = addr.port;
  });

  afterEach(() => srv.close());

  it("invoke proxies request body to backend", async () => {
    const base = `http://${host()}/v2/projects/${PROJECT}/locations/loc/services`;
    expect((await j("POST", base, { name: "ts-svc", backendUrl: `http://127.0.0.1:${port}/` })).status).toBe(200);
    const r = await j("POST", `${base}/ts-svc/invoke`, { echo: "ts" });
    expect(r.status).toBe(200);
    expect(r.body).toEqual({ echo: "ts" });
  });

  it("invoke without backendUrl returns 400", async () => {
    const base = `http://${host()}/v2/projects/${PROJECT}/locations/loc/services`;
    expect((await j("POST", base, { name: "ts-empty" })).status).toBe(200);
    const r = await j("POST", `${base}/ts-empty/invoke`);
    expect(r.status).toBe(400);
  });
});

describe("dashboard", () => {
  it("serves html and state api", async () => {
    const html = await fetch(`http://${host()}/dashboard`);
    expect(html.status).toBe(200);
    const ct = html.headers.get("content-type") || "";
    expect(ct).toContain("text/html");
    const state = await fetch(`http://${host()}/dashboard/api/state`);
    expect(state.status).toBe(200);
  });
});
