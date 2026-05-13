import { describe, expect, it } from "vitest";

describe("healthz", () => {
  it("reports services ready", async () => {
    const host = process.env.GCP_LOCAL_HOST;
    expect(host).toBeTruthy();
    const res = await fetch(`http://${host}/healthz`);
    expect(res.status).toBe(200);
    const body = (await res.json()) as {
      status: string;
      services: Record<string, string>;
    };
    expect(body.status).toBe("ok");
    expect(body.services.storage).toBe("ready");
    expect(body.services.pubsub).toBe("ready");
  });
});
