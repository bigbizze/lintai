import { missing } from "./missing";
export const lazy = () => import("./ok");
export const computed = (name: string) => import(name);
export { missing };
