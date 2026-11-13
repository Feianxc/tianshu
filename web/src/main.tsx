import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "@fontsource-variable/instrument-sans/standard.css";
import "@fontsource-variable/martian-mono/standard.css";
import "./styles.css";
import "./insight.css";
import { App } from "./app";
import { applyTheme, loadTheme } from "./lib";

applyTheme(loadTheme());
// OAuth sends the browser to /consent/<id> (a real path survives a login proxy, a #fragment does not).
// Turn it into the console's hash route before anything renders.
const consent = location.pathname.match(/^\/consent\/([A-Za-z0-9_-]+)$/);
if (consent) history.replaceState(null, "", `/#/oauth/${consent[1]}`);
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
