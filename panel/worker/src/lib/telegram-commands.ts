import type { Env } from "../types";
import { all, userIDFromName } from "./db";
import {
  answerCallbackQuery,
  editMessageText,
  escapeHtml,
  sendMessage,
  type InlineKeyboard,
  type TgUpdate
} from "./telegram";
import { createUserByName, deleteUser, listUsers, userSubscription, userUpgradeNodes } from "../routes/users";
import { nodeHealthcheck, nodeRotateCore, nodeStatus, nodeSyncCore } from "../routes/nodes";

export interface ParsedCommand {
  cmd: string;
  arg: string;
}

export interface ParsedCallback {
  entity: string;
  action: string;
  id: string;
  confirmed: boolean;
}

export function parseCommand(text: string): ParsedCommand | null {
  const trimmed = text.trim();
  if (!trimmed.startsWith("/")) {
    return null;
  }
  const sp = trimmed.indexOf(" ");
  const head = sp === -1 ? trimmed.slice(1) : trimmed.slice(1, sp);
  const arg = sp === -1 ? "" : trimmed.slice(sp + 1).trim();
  const cmd = head.split("@")[0].toLowerCase();
  if (!cmd) {
    return null;
  }
  return { cmd, arg };
}

export function parseCallback(data: string): ParsedCallback | null {
  const parts = data.split(":");
  if (parts.length < 3) {
    return null;
  }
  const [entity, action, id, suffix] = parts;
  if (!entity || !action || !id) {
    return null;
  }
  return { entity, action, id, confirmed: suffix === "yes" };
}
