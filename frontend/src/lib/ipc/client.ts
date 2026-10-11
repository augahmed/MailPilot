import { invoke, isTauri } from "@tauri-apps/api/core";
import { z } from "zod";

export const healthSchema = z
  .object({
    name: z.literal("MailPilot"),
    version: z.string().min(1),
    protocolVersion: z.literal(1),
    backend: z.literal("connected"),
    database: z.enum(["connected", "disconnected"]),
    schemaVersion: z.number().int().nonnegative(),
  })
  .strict();

export type Health = z.infer<typeof healthSchema>;

export async function getHealth(): Promise<Health> {
  if (!isTauri())
    throw new Error("Open the desktop app to connect to the backend.");
  return healthSchema.parse(await invoke<unknown>("health"));
}
