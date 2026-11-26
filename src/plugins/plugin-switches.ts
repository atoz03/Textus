import { forgetPluginSettings } from "./store/plugin-settings";
import { plugins as pluginsIpc, type PluginFile } from "../platform/ipc";
import { contributingPlugins } from "./contributions/plugin-contributions";
import { canRun, canSwitch, nextStatus } from "./store/lifecycle";
import {
  pluginRecord,
  pluginMayRun,
  pluginsSnapshot,
  publish,
  update,
} from "./store/plugin-records";
import { hosts, stopHost } from "./running-hosts";
import { apply, loadPlugins, withdraw } from "./manager";

/**
 * Switching plugins on and off, installing and removing: each told to the
 * backend, which owns the state, and applied in this window (loadPlugins
 * brings the others in line).
 */

/** Settings' switch for one plugin. Turned on, it has what its manifest
 *  lists: installing and switching it on is the user's yes, asked once
 *  there and not again permission by permission. */
export async function setPluginEnabled(id: string, on: boolean): Promise<void> {
  const record = pluginRecord(id);
  if (!record || !record.manifest) return;
  if (!canSwitch(record.status)) return;
  if (!on) {
    // Marked off first: a message from its open page meanwhile starts
    // nothing that would outlive the switch.
    update(id, {
      status: nextStatus(record.status, { type: "disable" }),
      problem: undefined,
    });
    stopHost(id);
    withdraw(id);
    await pluginsIpc.set(id, false, record.granted);
    return;
  }
  const granted = [...record.manifest.permissions];
  await pluginsIpc.set(id, true, granted);
  const next = {
    ...record,
    granted,
    status: nextStatus(record.status, { type: "enable" }),
    problem: undefined,
  };
  update(id, next);
  if (pluginMayRun(next)) apply(next);
}

/** Settings › Extensions › Plugins › Plugins: every plugin at once. */
export async function setAllPlugins(on: boolean): Promise<void> {
  if (!on) publish({ allOn: false });
  if (!on) for (const id of [...hosts.keys()]) stopHost(id);
  if (!on) for (const id of contributingPlugins()) withdraw(id);
  await pluginsIpc.setAll(on);
  publish({ allOn: on });
  for (const record of pluginsSnapshot().records)
    if (canRun(record.status, on)) apply(record);
}

export async function reloadPlugin(id: string): Promise<void> {
  stopHost(id);
  withdraw(id);
  await loadPlugins(true);
}

/** A package or folder at `source`, or files Textus AI wrote, installed. */
export async function installPlugin(
  source: string | PluginFile[],
): Promise<string> {
  const installed = await (typeof source === "string"
    ? pluginsIpc.install(source)
    : pluginsIpc.installFiles(source));
  stopHost(installed.id);
  await loadPlugins(true);
  return installed.id;
}

export async function loadDevPlugin(path: string): Promise<string> {
  const loaded = await pluginsIpc.loadDev(path);
  stopHost(loaded.id);
  await loadPlugins(true);
  return loaded.id;
}

export async function uninstallPlugin(id: string): Promise<void> {
  stopHost(id);
  withdraw(id);
  await forgetPluginSettings(id);
  await pluginsIpc.uninstall(id);
  await loadPlugins(true);
}
