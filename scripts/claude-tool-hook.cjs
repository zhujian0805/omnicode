#!/usr/bin/env node

const fs = require("node:fs")
const os = require("node:os")
const path = require("node:path")

const eventName = process.argv[2] || "unknown"
const defaultLogPath = path.join(os.homedir(), "tmp", "claude-tool-hooks.jsonl")
const logPath = process.env.CLAUDE_TOOL_HOOK_LOG || defaultLogPath

function ensureParentDir(filePath) {
  fs.mkdirSync(path.dirname(filePath), { recursive: true })
}

function readStdin() {
  try {
    return fs.readFileSync(0, "utf8")
  } catch {
    return ""
  }
}

function tryParseJSON(raw) {
  if (!raw || raw.trim() === "") {
    return null
  }

  try {
    return JSON.parse(raw)
  } catch {
    return null
  }
}

function pickFirst(object, keys) {
  if (object == null || typeof object !== "object") {
    return undefined
  }

  for (const key of keys) {
    if (Object.prototype.hasOwnProperty.call(object, key)) {
      return object[key]
    }
  }

  return undefined
}

function summarize(value, maxLength = 1600) {
  if (value == null) {
    return value
  }

  const text =
    typeof value === "string" ? value : (
      JSON.stringify(value, null, 2) || String(value)
    )

  if (text.length <= maxLength) {
    return text
  }

  return `${text.slice(0, maxLength - 3)}...`
}

function main() {
  const rawInput = readStdin()
  const parsedInput = tryParseJSON(rawInput)

  const toolName = pickFirst(parsedInput, [
    "tool_name",
    "toolName",
    "name",
    "tool",
  ])
  const toolInput = pickFirst(parsedInput, [
    "tool_input",
    "toolInput",
    "input",
    "tool_args",
    "toolArgs",
  ])
  const toolOutput = pickFirst(parsedInput, [
    "tool_output",
    "toolOutput",
    "output",
    "tool_response",
    "toolResponse",
    "response",
    "result",
  ])
  const error = pickFirst(parsedInput, [
    "error",
    "message",
    "failure_message",
    "failureMessage",
  ])
  const exitCode = pickFirst(parsedInput, ["exit_code", "exitCode", "code"])
  const sessionID = pickFirst(parsedInput, ["session_id", "sessionId"])
  const toolUseID = pickFirst(parsedInput, ["tool_use_id", "toolUseId", "id"])

  const record = {
    timestamp: new Date().toISOString(),
    event: eventName,
    cwd: process.cwd(),
    pid: process.pid,
    anthropicBaseURL: process.env.ANTHROPIC_BASE_URL || null,
    anthropicModel:
      process.env.CLAUDE_CODE_MODEL
      || process.env.ANTHROPIC_DEFAULT_HAIKU_MODEL
      || null,
    sessionID,
    toolUseID,
    toolName,
    exitCode,
    error: summarize(error),
    toolInputPreview: summarize(toolInput),
    toolOutputPreview: summarize(toolOutput),
    rawInputPreview: summarize(rawInput, 4000),
    parsedInput,
  }

  ensureParentDir(logPath)
  fs.appendFileSync(logPath, `${JSON.stringify(record)}\n`, "utf8")
}

try {
  main()
} catch (error) {
  try {
    ensureParentDir(logPath)
    fs.appendFileSync(
      logPath,
      `${JSON.stringify({
        timestamp: new Date().toISOString(),
        event: `${eventName}_logger_failure`,
        error: error instanceof Error ? error.message : String(error),
      })}\n`,
      "utf8",
    )
  } catch {
    // Ignore secondary failures to avoid breaking Claude Code hooks.
  }
}
