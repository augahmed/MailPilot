import { createContext, useContext } from "react";

// Presentation only. The backend independently enforces every demo restriction.
export const DemoContext = createContext(false);
export const useDemo = () => useContext(DemoContext);
