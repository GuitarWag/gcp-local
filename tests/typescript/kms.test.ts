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

describe("kms", () => {
  const base = () => `http://${host()}/v1/projects/${PROJECT}/locations/us/keyRings`;

  it("encrypt and decrypt round-trip", async () => {
    expect((await j("POST", `${base()}?keyRingId=ts-ring`)).status).toBe(200);
    expect((await j("POST", `${base()}/ts-ring/cryptoKeys?cryptoKeyId=k`, { purpose: "ENCRYPT_DECRYPT" })).status).toBe(200);

    const plain = Buffer.from("hello kms ts").toString("base64");
    const enc = await j("POST", `${base()}/ts-ring/cryptoKeys/k:encrypt`, { plaintext: plain });
    expect(enc.status).toBe(200);
    const ct: string = enc.body.ciphertext;
    expect(ct).toBeTruthy();

    const dec = await j("POST", `${base()}/ts-ring/cryptoKeys/k:decrypt`, { ciphertext: ct });
    expect(dec.status).toBe(200);
    expect(Buffer.from(dec.body.plaintext, "base64").toString()).toBe("hello kms ts");
  });

  it("rejects tampered ciphertext", async () => {
    expect((await j("POST", `${base()}?keyRingId=ts-tamper`)).status).toBe(200);
    expect((await j("POST", `${base()}/ts-tamper/cryptoKeys?cryptoKeyId=k`, {})).status).toBe(200);
    const enc = await j("POST", `${base()}/ts-tamper/cryptoKeys/k:encrypt`, {
      plaintext: Buffer.from("x").toString("base64"),
    });
    const raw = Buffer.from(enc.body.ciphertext, "base64");
    raw[raw.length - 1] ^= 0xff;
    const dec = await j("POST", `${base()}/ts-tamper/cryptoKeys/k:decrypt`, {
      ciphertext: raw.toString("base64"),
    });
    expect(dec.status).not.toBe(200);
  });
});
