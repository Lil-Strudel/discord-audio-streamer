<script lang="ts">
  import { untrack } from "svelte";
  import { SvelteMap, SvelteSet } from "svelte/reactivity";
  import { ListSoundFolder } from "../../wailsjs/go/main/App";
  import type { main } from "../../wailsjs/go/models";
  import { errorText } from "../lib/types";

  let {
    root,
    view,
    playing,
    disabled,
    onplay,
  }: {
    /** The folder chosen for the track. */
    root: string;
    view: string;
    /** The file the track is playing, highlighted wherever it appears. */
    playing: string;
    disabled: boolean;
    onplay: (path: string) => void;
  } = $props();

  // Listings are fetched a folder at a time and kept, so switching views or
  // stepping back up does not read the disk again. Refresh drops them.
  const listings = new SvelteMap<string, main.SoundEntry[]>();
  const failures = new SvelteMap<string, string>();
  const expanded = new SvelteSet<string>();

  // The grid's position below the root, one entry per folder entered.
  let trail = $state<main.SoundEntry[]>([]);
  let here = $derived(trail.at(-1)?.path ?? root);

  // A new root is a different library: nothing learned about the old one
  // applies, including where the grid had got to.
  $effect(() => {
    root;
    untrack(reset);
  });

  function reset() {
    listings.clear();
    failures.clear();
    expanded.clear();
    trail = [];
  }

  async function load(dir: string) {
    if (listings.has(dir)) return;
    try {
      listings.set(dir, (await ListSoundFolder(dir)) ?? []);
      failures.delete(dir);
    } catch (err) {
      failures.set(dir, errorText(err));
    }
  }

  // The grid's folder is loaded whenever it changes; the tree's folders are
  // loaded as they are expanded.
  let shown = $derived(view === "list" ? root : here);
  $effect(() => {
    const dir = shown;
    if (root) untrack(() => load(dir));
  });

  /** Reads the open folders again, keeping where the user is. */
  function refresh() {
    listings.clear();
    failures.clear();
    load(shown);
    for (const dir of expanded) load(dir);
  }

  function toggle(dir: string) {
    if (expanded.has(dir)) {
      expanded.delete(dir);
    } else {
      expanded.add(dir);
      load(dir);
    }
  }

  /** A button reads better without ".mp3" on the end; the tooltip keeps it. */
  function label(entry: main.SoundEntry) {
    if (entry.isDir) return entry.name;
    const dot = entry.name.lastIndexOf(".");
    return dot > 0 ? entry.name.slice(0, dot) : entry.name;
  }

  function baseName(path: string) {
    return path.split(/[\\/]/).filter(Boolean).at(-1) ?? path;
  }
</script>

<div class="browser">
  {#if view === "grid"}
    <div class="crumbs">
      <button
        class="crumb"
        class:current={trail.length === 0}
        title={root}
        onclick={() => (trail = [])}
      >
        {baseName(root)}
      </button>
      {#each trail as step, i (step.path)}
        <span class="sep">›</span>
        <button
          class="crumb"
          class:current={i === trail.length - 1}
          onclick={() => (trail = trail.slice(0, i + 1))}
        >
          {step.name}
        </button>
      {/each}
      <button class="refresh" title="Reload the folder" aria-label="Reload the folder" onclick={refresh}>
        ⟳
      </button>
    </div>

    {#if failures.has(here)}
      <p class="note bad">{failures.get(here)}</p>
    {:else if !listings.has(here)}
      <p class="note">Loading…</p>
    {:else if listings.get(here)!.length === 0}
      <p class="note">No sounds or folders here.</p>
    {:else}
      <div class="grid">
        {#if trail.length > 0}
          <button class="tile folder" onclick={() => (trail = trail.slice(0, -1))}>
            ↩ Back
          </button>
        {/if}
        {#each listings.get(here)! as entry (entry.path)}
          {#if entry.isDir}
            <button
              class="tile folder"
              title={entry.name}
              onclick={() => (trail = [...trail, entry])}
            >
              📁 {entry.name}
            </button>
          {:else}
            <button
              class="tile"
              class:playing={entry.path === playing}
              title={entry.name}
              {disabled}
              onclick={() => onplay(entry.path)}
            >
              {label(entry)}
            </button>
          {/if}
        {/each}
      </div>
    {/if}
  {:else}
    <div class="crumbs">
      <span class="crumb current" title={root}>{baseName(root)}</span>
      <button class="refresh" title="Reload the folder" aria-label="Reload the folder" onclick={refresh}>
        ⟳
      </button>
    </div>
    <ul class="tree">
      {@render branch(root, 0)}
    </ul>
  {/if}
</div>

{#snippet branch(dir: string, depth: number)}
  {#if failures.has(dir)}
    <li class="note bad" style="--depth: {depth}">{failures.get(dir)}</li>
  {:else if !listings.has(dir)}
    <li class="note" style="--depth: {depth}">Loading…</li>
  {:else if listings.get(dir)!.length === 0}
    <li class="note" style="--depth: {depth}">Empty</li>
  {:else}
    {#each listings.get(dir)! as entry (entry.path)}
      <li>
        {#if entry.isDir}
          <button
            class="row folder"
            style="--depth: {depth}"
            aria-expanded={expanded.has(entry.path)}
            onclick={() => toggle(entry.path)}
          >
            <span class="twisty">{expanded.has(entry.path) ? "▾" : "▸"}</span>
            📁 {entry.name}
          </button>
          {#if expanded.has(entry.path)}
            <ul>{@render branch(entry.path, depth + 1)}</ul>
          {/if}
        {:else}
          <button
            class="row"
            class:playing={entry.path === playing}
            style="--depth: {depth}"
            title={entry.name}
            {disabled}
            onclick={() => onplay(entry.path)}
          >
            <span class="twisty">♪</span>
            {entry.name}
          </button>
        {/if}
      </li>
    {/each}
  {/if}
{/snippet}

<style>
  .browser {
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-height: 0;
  }

  .crumbs {
    display: flex;
    align-items: center;
    gap: 2px;
    min-width: 0;
    font-size: 12.5px;
  }

  .crumb {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    background: none;
    border: none;
    padding: 2px 4px;
    color: var(--text-dim);
  }

  .crumb:hover:not(:disabled) {
    background: none;
    color: var(--text);
  }

  .crumb.current {
    color: var(--text);
  }

  .sep {
    color: var(--text-faint);
  }

  .refresh {
    margin-left: auto;
    background: none;
    border: none;
    padding: 0 6px;
    color: var(--text-faint);
  }

  .refresh:hover:not(:disabled) {
    background: none;
    color: var(--text);
  }

  /* The browser is the only part of a track that grows with the library, so it
     scrolls rather than pushing the next track down the page. */
  .grid,
  .tree {
    max-height: 220px;
    overflow-y: auto;
  }

  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(104px, 1fr));
    gap: 6px;
  }

  .tile {
    height: 52px;
    padding: 4px 8px;
    font-size: 12.5px;
    line-height: 1.25;
    overflow: hidden;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    word-break: break-word;
  }

  .tile.folder {
    background: var(--surface);
    color: var(--text-dim);
  }

  .tile.playing,
  .row.playing {
    border-color: var(--accent);
    background: color-mix(in srgb, var(--accent) 22%, var(--surface-raised));
  }

  .tree,
  .tree ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .tree {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: var(--radius-sm);
  }

  .row {
    display: flex;
    align-items: center;
    gap: 6px;
    width: 100%;
    padding: 3px 8px 3px calc(8px + var(--depth) * 16px);
    background: none;
    border: 1px solid transparent;
    border-radius: 0;
    font-size: 12.5px;
    text-align: left;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .row:hover:not(:disabled) {
    background: var(--surface-raised);
    border-color: transparent;
  }

  .row.folder {
    color: var(--text-dim);
  }

  .twisty {
    width: 12px;
    flex: none;
    color: var(--text-faint);
    text-align: center;
  }

  .note {
    margin: 0;
    padding: 6px 8px;
    font-size: 12.5px;
    color: var(--text-faint);
  }

  li.note {
    padding-left: calc(26px + var(--depth) * 16px);
  }

  .note.bad {
    color: var(--warn);
  }
</style>
