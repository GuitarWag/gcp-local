import mysql from "mysql2/promise";
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

describe("cloudsql mysql wire", () => {
  it("schema + crud via mysql2", async () => {
    const base = `http://${host()}/sql/v1beta4/projects/${PROJECT}/instances`;
    const created = await j("POST", base, {
      name: "ts-mysql-wire",
      engine: "mysql",
      database: "appdb",
    });
    expect(created.status).toBe(200);
    expect(created.body.port).toBeGreaterThan(0);
    expect(created.body.host).toBe("127.0.0.1");

    const conn = await mysql.createConnection({
      host: created.body.host,
      port: created.body.port,
      database: "appdb",
      user: "gcp-local",
      password: "local",
    });
    try {
      await conn.query(
        "CREATE TABLE entries (id INT PRIMARY KEY, body VARCHAR(100), score INT) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
      );
      await conn.execute(
        "INSERT INTO entries (id, body, score) VALUES (?, ?, ?)",
        [1, "one", 10],
      );
      await conn.execute(
        "INSERT INTO entries (id, body, score) VALUES (?, ?, ?)",
        [2, "two", 20],
      );

      const [sel] = await conn.execute<mysql.RowDataPacket[]>(
        "SELECT id, body, score FROM entries WHERE id = ?",
        [1],
      );
      expect(sel).toEqual([{ id: 1, body: "one", score: 10 }]);

      const [upd] = await conn.execute<mysql.ResultSetHeader>(
        "UPDATE entries SET score = ? WHERE body = ?",
        [15, "one"],
      );
      expect(upd.affectedRows).toBe(1);

      const [all] = await conn.execute<mysql.RowDataPacket[]>(
        "SELECT body FROM entries ORDER BY id",
      );
      expect(all.map((r) => r.body)).toEqual(["one", "two"]);

      const [del] = await conn.execute<mysql.ResultSetHeader>(
        "DELETE FROM entries WHERE id = ?",
        [2],
      );
      expect(del.affectedRows).toBe(1);
    } finally {
      await conn.end();
    }
  });
});
