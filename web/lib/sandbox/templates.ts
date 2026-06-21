"use client";

// templates.ts — offline mirror of the built-in templates in phone_backend.go
// (templateSchema/templateAuth/templateSeed/templateApp/ListPhoneTemplates).
// Lets the browser sandbox create a project with no agent and no network.
// Keep in sync with desktop/agent/phone_backend.go:1440-1631.

import type {
  PhoneAppSpec,
  PhoneAuth,
  PhoneSchema,
  PhoneSeed,
  PhoneTemplate,
} from "@/lib/agent-client";

export const TEMPLATES: PhoneTemplate[] = [
  { id: "blank", label: "Blank", description: "Empty project — define your own schema." },
  { id: "crud", label: "Generic CRUD", description: "users + items table with a few personas." },
  { id: "todos", label: "Todos", description: "users + todos with seeded tasks." },
  { id: "notes", label: "Notes", description: "users + notes with a starter entry." },
];

const USERS_TABLE = {
  name: "users",
  columns: [
    { name: "id", type: "text", primary: true },
    { name: "email", type: "text", required: true, unique: true },
    { name: "name", type: "text" },
  ],
};

export function templateSchema(name: string): PhoneSchema {
  switch (name) {
    case "todos":
      return {
        tables: [
          USERS_TABLE,
          {
            name: "todos",
            columns: [
              { name: "id", type: "text", primary: true, default: "uuid" },
              { name: "title", type: "text", required: true },
              { name: "done", type: "bool", default: "false" },
              { name: "owner_id", type: "text" },
              { name: "created_at", type: "timestamp", default: "now" },
            ],
            indexes: [{ columns: ["owner_id"] }, { columns: ["done"] }],
          },
        ],
        relations: [{ from: "todos.owner_id", to: "users.id", onDelete: "cascade" }],
      };
    case "notes":
      return {
        tables: [
          USERS_TABLE,
          {
            name: "notes",
            columns: [
              { name: "id", type: "text", primary: true, default: "uuid" },
              { name: "title", type: "text", required: true },
              { name: "body", type: "text" },
              { name: "owner_id", type: "text" },
              { name: "created_at", type: "timestamp", default: "now" },
              { name: "updated_at", type: "timestamp", default: "now" },
            ],
            indexes: [{ columns: ["owner_id"] }],
          },
        ],
      };
    case "blank":
      return { tables: [] };
    case "crud":
    default:
      return {
        tables: [
          USERS_TABLE,
          {
            name: "items",
            columns: [
              { name: "id", type: "text", primary: true, default: "uuid" },
              { name: "name", type: "text", required: true },
              { name: "description", type: "text" },
              { name: "owner_id", type: "text" },
              { name: "created_at", type: "timestamp", default: "now" },
            ],
          },
        ],
      };
  }
}

export function templateAuth(name: string): PhoneAuth {
  if (name === "blank") return { personas: [] };
  return {
    personas: [
      { id: "alice", email: "alice@example.com", name: "Alice" },
      { id: "bob", email: "bob@example.com", name: "Bob" },
    ],
  };
}

export function templateSeed(name: string): PhoneSeed {
  switch (name) {
    case "todos":
      return {
        todos: [
          { id: "t1", title: "Buy milk", done: false, owner_id: "alice" },
          { id: "t2", title: "Learn Yaver", done: true, owner_id: "alice" },
          { id: "t3", title: "Ship mini-backend", done: false, owner_id: "bob" },
        ],
      };
    case "notes":
      return {
        notes: [{ id: "n1", title: "Welcome", body: "This is a starter note.", owner_id: "alice" }],
      };
    case "crud":
      return {
        items: [{ id: "i1", name: "Example", description: "Edit or delete this row.", owner_id: "alice" }],
      };
    default:
      return {};
  }
}

export function templateApp(name: string): PhoneAppSpec {
  switch (name) {
    case "todos":
      return {
        summary: "Simple shared todo list with a quick capture flow.",
        primaryEntity: "todos",
        screens: [
          {
            id: "todo_list",
            title: "Todos",
            kind: "list",
            table: "todos",
            emptyState: "No tasks yet. Add one from your phone.",
            actions: [
              { label: "Add task", kind: "create", table: "todos" },
              { label: "Toggle done", kind: "update", table: "todos" },
            ],
          },
        ],
      };
    case "notes":
      return {
        summary: "Lightweight notes app with a notes list and editor.",
        primaryEntity: "notes",
        screens: [
          {
            id: "notes_list",
            title: "Notes",
            kind: "list",
            table: "notes",
            emptyState: "Start with a quick note.",
            actions: [
              { label: "New note", kind: "create", table: "notes" },
              { label: "Open note", kind: "navigate", target: "note_detail" },
            ],
          },
        ],
      };
    case "blank":
      return { summary: "Blank app. Define screens after shaping the schema." };
    case "crud":
    default:
      return {
        summary: "Generic CRUD app with a collection list and editor.",
        primaryEntity: "items",
        screens: [
          {
            id: "items_list",
            title: "Items",
            kind: "list",
            table: "items",
            emptyState: "Create the first item.",
            actions: [
              { label: "Create item", kind: "create", table: "items" },
              { label: "View item", kind: "navigate", target: "item_detail" },
            ],
          },
        ],
      };
  }
}
