import { describe, expect, it } from "vitest";

const PROJECT = "local-project";

const CATALOG = [
  "bigquery.googleapis.com",
  "bigtable.googleapis.com",
  "cloudfunctions.googleapis.com",
  "cloudkms.googleapis.com",
  "cloudscheduler.googleapis.com",
  "cloudtasks.googleapis.com",
  "firestore.googleapis.com",
  "iam.googleapis.com",
  "iamcredentials.googleapis.com",
  "logging.googleapis.com",
  "monitoring.googleapis.com",
  "pubsub.googleapis.com",
  "redis.googleapis.com",
  "run.googleapis.com",
  "secretmanager.googleapis.com",
  "serviceusage.googleapis.com",
  "spanner.googleapis.com",
  "sqladmin.googleapis.com",
  "storage.googleapis.com",
];

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

describe("serviceusage", () => {
  it("lists all 19 catalog services as ENABLED", async () => {
    const r = await j("GET", `http://${host()}/v1/projects/${PROJECT}/services`);
    expect(r.status).toBe(200);
    const names = r.body.services.map((s: { name: string }) => s.name.split("/").pop());
    for (const name of CATALOG) {
      expect(names).toContain(name);
    }
    for (const svc of r.body.services) {
      expect(svc.state).toBe("ENABLED");
      expect(svc.config.name).toBe(svc.name.split("/").pop());
    }
  });

  it("batchEnable with more than 20 ids returns 400 INVALID_ARGUMENT", async () => {
    const ids = [...CATALOG, "extra-one.googleapis.com", "extra-two.googleapis.com"];
    expect(ids.length).toBe(21);
    const r = await j("POST", `http://${host()}/v1/projects/${PROJECT}/services:batchEnable`, {
      serviceIds: ids,
    });
    expect(r.status).toBe(400);
    expect(r.body.error.status).toBe("INVALID_ARGUMENT");
  });
});
