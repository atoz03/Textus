import { expect, it, vi } from "vitest";

const { set, updated } = vi.hoisted(() => ({
  set: vi.fn(async (...args: unknown[]) => void args),
  updated: [] as unknown[],
}));
vi.mock("../platform/ipc", () => ({ plugins: { set } }));
vi.mock("./manager", () => ({
  apply: () => {},
  loadPlugins: async () => {},
  withdraw: () => {},
}));
vi.mock("./running-hosts", () => ({ hosts: new Map(), stopHost: () => {} }));
vi.mock("./store/plugin-records", () => ({
  pluginRecord: () => ({
    id: "p",
    status: "off",
    granted: [],
    manifest: { permissions: ["files.read", "network:api.example.com"] },
  }),
  pluginMayRun: () => false,
  pluginsSnapshot: () => ({ allOn: true }),
  publish: () => {},
  update: (_id: string, patch: unknown) => updated.push(patch),
}));
const confirm = vi.hoisted(() => vi.fn());
vi.mock("../engine/controls/app-dialog", () => ({ showConfirm: confirm }));

import { setPluginEnabled } from "./plugin-switches";

it("switches a plugin on with everything its manifest lists, asking nothing", async () => {
  await setPluginEnabled("p", true);
  const all = ["files.read", "network:api.example.com"];
  expect(set).toHaveBeenCalledWith("p", true, all);
  expect(updated).toEqual([
    expect.objectContaining({ status: "on", granted: all }),
  ]);
  expect(confirm).not.toHaveBeenCalled();
});
