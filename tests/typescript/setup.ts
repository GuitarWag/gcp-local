import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { createServer } from "node:net";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

const REPO_ROOT = resolve(__dirname, "..", "..");

let proc: ChildProcess | undefined;

function freePort(): Promise<number> {
  return new Promise((resolveFn, rejectFn) => {
    const srv = createServer();
    srv.unref();
    srv.on("error", rejectFn);
    srv.listen(0, () => {
      const addr = srv.address();
      if (!addr || typeof addr === "string") {
        rejectFn(new Error("no port"));
        return;
      }
      const port = addr.port;
      srv.close(() => resolveFn(port));
    });
  });
}

async function waitReady(host: string, timeoutMs = 5000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`http://${host}/healthz`);
      if (res.status === 200) return;
    } catch {
      // ignore
    }
    await delay(50);
  }
  throw new Error(`gcp-local not ready at ${host} within ${timeoutMs}ms`);
}

function buildBinary(): string {
  const dir = mkdtempSync(join(tmpdir(), "gcp-local-bin-"));
  const binPath = join(dir, "gcp-local");
  const result = spawnSync("go", ["build", "-o", binPath, "./cmd/gcp-local"], {
    cwd: REPO_ROOT,
    stdio: "inherit",
  });
  if (result.status !== 0) {
    throw new Error(`go build failed with status ${result.status}`);
  }
  return binPath;
}

export async function setup(): Promise<void> {
  const binPath = buildBinary();
  const port = await freePort();
  const host = `localhost:${port}`;
  proc = spawn(binPath, ["start", `--port=${port}`, "--no-daemon"], {
    stdio: "ignore",
    detached: false,
  });
  await waitReady(host);
  process.env.GCP_LOCAL_HOST = host;
}

export async function teardown(): Promise<void> {
  if (proc) {
    proc.kill("SIGTERM");
    await delay(100);
    if (!proc.killed) proc.kill("SIGKILL");
  }
}
