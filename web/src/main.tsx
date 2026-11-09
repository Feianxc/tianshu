import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "@fontsource-variable/instrument-sans/standard.css";
import "@fontsource-variable/martian-mono/standard.css";
import "./styles.css";
import { App } from "./app";
import { applyTheme, loadTheme } from "./lib";

applyTheme(loadTheme());
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
