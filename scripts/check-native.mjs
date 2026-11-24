#!/usr/bin/env node
// The native checks: gofmt, go vet and the Go backend's tests. The app
// embeds dist/, so a missing one is built first.
import { spawnSync } from "node:child_process";
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");

function run(command, args, cwd) {
  console.log(`\n> ${command} ${args.join(" ")}`);
  const result = spawnSync(command, args, {
    cwd,
    stdio: ["inherit", "pipe", "inherit"],
    encoding: "utf8",
    // npm is npm.cmd on Windows, which only a shell resolves.
    shell: process.platform === "win32",
  });
  if (result.error) {
    console.error(`could not run ${command}: ${result.error.message}`);
    process.exit(1);
  }
  process.stdout.write(result.stdout ?? "");
  if (result.status !== 0) process.exit(result.status ?? 1);
  return result.stdout ?? "";
}

if (!existsSync(join(root, "dist", "index.html")))
  run("npm", ["run", "build"], root);

const unformatted = run("gofmt", ["-l", "main.go", "backend"], root).trim();
if (unformatted) {
  console.error("not gofmt-formatted (run gofmt -w on them)");
  process.exit(1);
}
run("go", ["vet", ".", "./backend/..."], root);
run("go", ["test", "./backend/..."], root);
