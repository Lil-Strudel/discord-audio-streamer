import { mount } from "svelte";
import "./style.css";
import App from "./App.svelte";

// Svelte 5 mounts components through mount() rather than `new Component()`,
// which is what the Wails template still generates.
const app = mount(App, {
  target: document.getElementById("app")!,
});

export default app;
