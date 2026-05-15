import { Client } from "pg";
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

describe("cloudsql pg wire", () => {
  it("schema + crud via node-postgres", async () => {
    const base = `http://${host()}/sql/v1beta4/projects/${PROJECT}/instances`;
    const created = await j("POST", base, {
      name: "ts-wire",
      databaseVersion: "POSTGRES_15",
      engine: "sqlite",
      database: "appdb",
    });
    expect(created.status).toBe(200);
    expect(created.body.port).toBeGreaterThan(0);
    expect(created.body.host).toBe("127.0.0.1");

    const client = new Client({
      host: created.body.host,
      port: created.body.port,
      database: "appdb",
      user: "gcp-local",
      password: "local",
      ssl: false,
    });
    await client.connect();
    try {
      await client.query(
        "CREATE TABLE entries (id INTEGER PRIMARY KEY, body TEXT, score INTEGER)",
      );
      await client.query(
        "INSERT INTO entries (id, body, score) VALUES ($1, $2, $3)",
        [1, "one", 10],
      );
      await client.query(
        "INSERT INTO entries (id, body, score) VALUES ($1, $2, $3)",
        [2, "two", 20],
      );

      const sel = await client.query(
        "SELECT id, body, score FROM entries WHERE id = $1",
        [1],
      );
      expect(sel.rows).toEqual([{ id: 1, body: "one", score: 10 }]);

      const upd = await client.query(
        "UPDATE entries SET score = $1 WHERE body = $2",
        [15, "one"],
      );
      expect(upd.rowCount).toBe(1);

      const all = await client.query(
        "SELECT body FROM entries ORDER BY id",
      );
      expect(all.rows.map((r) => r.body)).toEqual(["one", "two"]);

      const del = await client.query(
        "DELETE FROM entries WHERE id = $1",
        [2],
      );
      expect(del.rowCount).toBe(1);
    } finally {
      await client.end();
    }
  });
});
