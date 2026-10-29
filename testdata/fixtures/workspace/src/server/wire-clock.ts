import "server-only";
import { wired } from "../pure/wired";
import { now } from "../services/clock";

export const reading = wired(now);
