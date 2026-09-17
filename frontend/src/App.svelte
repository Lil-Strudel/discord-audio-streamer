<script lang="ts">
  import { EventsOn } from "../wailsjs/runtime/runtime";
  import { ClearToken, LogPath, OpenLogFolder, Queue, Status } from "../wailsjs/go/main/App";
  import type { main, playlist } from "../wailsjs/go/models";
  import { emptyQueue, emptyTelemetry, errorText, type Telemetry } from "./lib/types";

  import ConnectionBar from "./components/ConnectionBar.svelte";
  import PlayerView from "./components/PlayerView.svelte";
  import StreamView from "./components/StreamView.svelte";
  import TokenSetup from "./components/TokenSetup.svelte";

  type Screen = "loading" | "onboarding" | "main" | "settings";

  let screen = $state<Screen>("loading");
  let status = $state<main.Status | null>(null);
  let telemetry = $state<Telemetry>(emptyTelemetry);
  let queue = $state<playlist.State>(emptyQueue);
  let tab = $state<"player" | "stream">("player");
  let banner = $state("");
  let logPath = $state("");

  async function openLogs() {
    try {
      await OpenLogFolder();
    } catch (err) {
      banner = errorText(err);
    }
  }

  async function refresh() {
    try {
      status = await Status();
      if (screen === "loading") {
        screen = status.hasToken ? "main" : "onboarding";
      }
    } catch (err) {
      banner = errorText(err);
    }
  }

  // The backend pushes status whenever the shape of the UI should change, and
  // telemetry continuously while audio flows. Polling either would either lag
  // the meters or waste work while idle.
  //
  // The queue is separate from the status because it is large and changes
  // rarely, so it is not worth re-sending every time the volume moves.
  EventsOn("status", (s: main.Status) => (status = s));
  EventsOn("telemetry", (t: Telemetry) => (telemetry = t));
  EventsOn("queue", (q: playlist.State) => (queue = q));
  EventsOn("error", (message: string) => (banner = message));

  refresh();
  Queue()
    .then((q: playlist.State) => (queue = q))
    .catch(() => {});
  LogPath()
    .then((path: string) => (logPath = path))
    .catch(() => {});

  async function resetToken() {
    try {
      await ClearToken();
      await refresh();
      screen = "onboarding";
    } catch (err) {
      banner = errorText(err);
    }
  }
</script>

<main>
  {#if screen === "loading" || !status}
    <div class="centre"><p>Starting…</p></div>
  {:else if screen === "onboarding"}
    <TokenSetup
      onDone={async () => {
        await refresh();
        screen = "main";
      }}
    />
  {:else if screen === "settings"}
    <div class="settings">
      <TokenSetup
        heading="Change your bot"
        finishLabel="Save token"
        onCancel={() => (screen = "main")}
        onDone={async () => {
          await refresh();
          screen = "main";
        }}
      />
      <div class="row">
        <div>
          <strong>Application logs</strong>
          <p>
            What the app recorded, including the previous run. Worth attaching
            to a bug report.
            {#if logPath}<code>{logPath}</code>{/if}
          </p>
        </div>
        <button onclick={openLogs}>Open log folder</button>
      </div>
      <div class="danger-zone">
        <div>
          <strong>Forget the saved token</strong>
          <p>The app will disconnect and return to setup.</p>
        </div>
        <button class="danger" onclick={resetToken}>Forget token</button>
      </div>
    </div>
  {:else}
    <ConnectionBar
      {status}
      {telemetry}
      onError={(m) => (banner = m)}
      onSettings={() => (screen = "settings")}
    />

    <nav>
      <button class:selected={tab === "player"} onclick={() => (tab = "player")}>
        Playlist
      </button>
      <button class:selected={tab === "stream"} onclick={() => (tab = "stream")}>
        Stream desktop audio
      </button>
    </nav>

    <section>
      {#if tab === "player"}
        <PlayerView {status} {telemetry} {queue} onError={(m) => (banner = m)} />
      {:else}
        <StreamView {status} {telemetry} onError={(m) => (banner = m)} />
      {/if}
    </section>
  {/if}

  {#if status?.ffmpegError}
    <div class="banner error">
      <span>{status.ffmpegError}</span>
    </div>
  {/if}

  {#if banner}
    <div class="banner error">
      <span>{banner}</span>
      <button onclick={() => (banner = "")} aria-label="Dismiss">×</button>
    </div>
  {/if}
</main>

<style>
  main {
    display: flex;
    flex-direction: column;
    height: 100%;
    overflow: hidden;
  }

  .centre {
    display: grid;
    place-items: center;
    height: 100%;
    color: var(--text-faint);
  }

  .settings {
    overflow-y: auto;
  }

  /* The log row and the danger zone are the same shape; only the accent on
     the button differs. */
  .row,
  .danger-zone {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    max-width: 620px;
    margin: 0 auto 16px;
    padding: 14px 18px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius);
  }

  .danger-zone {
    margin-bottom: 48px;
  }

  .row code {
    display: block;
    margin-top: 4px;
    font-size: 11.5px;
    color: var(--text-faint);
    word-break: break-all;
  }

  .row p,
  .danger-zone p {
    margin: 2px 0 0;
    font-size: 12.5px;
    color: var(--text-faint);
  }

  nav {
    display: flex;
    gap: 4px;
    padding: 0 20px;
    background: var(--surface);
    border-bottom: 1px solid var(--border);
  }

  nav button {
    background: none;
    border: none;
    border-bottom: 2px solid transparent;
    border-radius: 0;
    padding: 11px 14px;
    color: var(--text-dim);
  }

  nav button:hover {
    background: none;
    color: var(--text);
  }

  nav button.selected {
    color: var(--text);
    border-bottom-color: var(--accent);
  }

  section {
    flex: 1;
    overflow-y: auto;
    padding: 24px 20px;
  }

  .banner {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 10px 16px;
    font-size: 13px;
  }

  .banner.error {
    background: color-mix(in srgb, var(--bad) 18%, var(--surface));
    border-top: 1px solid color-mix(in srgb, var(--bad) 45%, transparent);
    color: var(--text);
  }

  .banner button {
    background: none;
    border: none;
    padding: 0 6px;
    font-size: 18px;
    line-height: 1;
    color: inherit;
  }

  .banner button:hover {
    background: none;
  }
</style>
