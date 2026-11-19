import { authorize } from "../security/check";

export const decisions = [1].map(authorize);
