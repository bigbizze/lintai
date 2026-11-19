import { authorize } from "../security/check";

function isPositive(value: number): boolean {
  return value > 0;
}

function applyLocally(callback: (value: number) => boolean): boolean {
  return callback(1);
}

export const decisions = [1].map(isPositive);
export const direct = authorize(1);
export const localCallback = applyLocally(authorize);
