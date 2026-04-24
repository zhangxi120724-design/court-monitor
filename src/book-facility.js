import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import dotenv from "dotenv";
import { chromium } from "playwright";

dotenv.config();

const STORAGE_DIR = path.resolve(".auth");
const STORAGE_PATH = path.join(STORAGE_DIR, "storage-state.json");

const config = {
  baseUrl: requireEnv("PERFECTGYM_BASE_URL"),
  loginUrl: process.env.PERFECTGYM_LOGIN_URL || "https://thekallang.perfectgym.com/clientportal2/",
  targetDate: resolveTargetDate(),
  targetTimeText: (process.env.TARGET_TIME_TEXT || "").trim(),
  targetCourtKeyword: (process.env.TARGET_COURT_KEYWORD || "indoor").trim().toLowerCase(),
  email: (process.env.PERFECTGYM_EMAIL || "").trim(),
  password: (process.env.PERFECTGYM_PASSWORD || "").trim(),
  headless: parseBoolean(process.env.HEADLESS, false),
  pollIntervalMs: parseInteger(process.env.POLL_INTERVAL_MS, 1500),
  maxWaitMs: parseInteger(process.env.MAX_WAIT_MS, 120000)
};

async function main() {
  fs.mkdirSync(STORAGE_DIR, { recursive: true });

  const browser = await chromium.launch({ headless: config.headless });
  const context = await browser.newContext(loadStorageState());
  const page = await context.newPage();

  try {
    await ensureLoggedIn(page, context);
    await page.goto(config.baseUrl, { waitUntil: "domcontentloaded" });
    await dismissCommonPopups(page);
    await navigateToTargetDate(page, config.targetDate);

    const slot = await pollForTargetSlot(page, config.maxWaitMs, config.pollIntervalMs);
    if (!slot) {
      throw new Error(`No matching slot found before timeout (${config.maxWaitMs} ms).`);
    }

    await addSlotToCart(slot);
    await context.storageState({ path: STORAGE_PATH });
    console.log("Success: matching facility slot was added to cart.");
  } finally {
    await browser.close();
  }
}

async function ensureLoggedIn(page, context) {
  await page.goto(config.baseUrl, { waitUntil: "domcontentloaded" });
  await dismissCommonPopups(page);

  if (!(await isLoginRequired(page))) {
    return;
  }

  console.log("Login is required.");

  const autoLoginWorked = await tryCredentialLogin(page);
  if (!autoLoginWorked) {
    console.log("Please finish login manually in the opened browser window.");
    await page.waitForFunction(() => {
      const text = document.body?.innerText?.toLowerCase() || "";
      return !text.includes("sign in") && !text.includes("log in") && !text.includes("login");
    }, { timeout: 5 * 60_000 });
  }

  await context.storageState({ path: STORAGE_PATH });
}

async function tryCredentialLogin(page) {
  if (!config.email || !config.password) {
    return false;
  }

  const emailInput = page.locator('input[type="email"], input[name*="email" i], input[autocomplete="username"]').first();
  const passwordInput = page.locator('input[type="password"]').first();

  if (!(await emailInput.isVisible().catch(() => false)) || !(await passwordInput.isVisible().catch(() => false))) {
    return false;
  }

  await emailInput.fill(config.email);
  await passwordInput.fill(config.password);

  const submitButton = page.getByRole("button", { name: /sign in|log in|login/i }).first();
  if (await submitButton.isVisible().catch(() => false)) {
    await submitButton.click();
  } else {
    await passwordInput.press("Enter");
  }

  await page.waitForLoadState("networkidle", { timeout: 30_000 }).catch(() => null);
  return !(await isLoginRequired(page));
}

async function isLoginRequired(page) {
  const bodyText = (await page.locator("body").innerText().catch(() => "")).toLowerCase();
  return /(sign in|log in|login|password)/i.test(bodyText);
}

async function navigateToTargetDate(page, targetDate) {
  const targetLabel = formatDateLabel(targetDate);
  console.log(`Navigating to target date: ${targetDate} (${targetLabel})`);

  const directDateCell = page.getByText(new RegExp(`${escapeRegExp(targetLabel)}|${escapeRegExp(targetDate)}`, "i")).first();
  if (await directDateCell.isVisible().catch(() => false)) {
    await directDateCell.click();
    return;
  }

  const maxMonthMoves = 12;
  for (let i = 0; i < maxMonthMoves; i += 1) {
    const exactDay = targetDate.slice(-2).replace(/^0/, "");
    const dayButton = page.getByRole("button", { name: new RegExp(`^${exactDay}$`) }).first();
    if (await dayButton.isVisible().catch(() => false)) {
      await dayButton.click();
      return;
    }

    const nextButton = page.getByRole("button", { name: /next|>|chevron right|arrow right/i }).first();
    if (!(await nextButton.isVisible().catch(() => false))) {
      break;
    }
    await nextButton.click();
    await page.waitForTimeout(500);
  }

  console.log("Date picker fallback: could not confidently switch date, continuing with current visible date.");
}

async function pollForTargetSlot(page, maxWaitMs, pollIntervalMs) {
  const deadline = Date.now() + maxWaitMs;

  while (Date.now() < deadline) {
    await dismissCommonPopups(page);

    const slot = await findMatchingSlot(page);
    if (slot) {
      console.log("Found matching slot.");
      return slot;
    }

    await tryRefreshAvailability(page);
    await page.waitForTimeout(pollIntervalMs);
  }

  return null;
}

async function findMatchingSlot(page) {
  const candidateSelectors = [
    '[class*="slot"]',
    '[class*="booking"]',
    '[class*="facility"]',
    '[class*="resource"]',
    'button:has-text("Book")',
    'button:has-text("Add")'
  ];

  for (const selector of candidateSelectors) {
    const candidates = page.locator(selector);
    const count = await candidates.count().catch(() => 0);

    for (let i = 0; i < count; i += 1) {
      const candidate = candidates.nth(i);
      const text = (await candidate.innerText().catch(() => "")).toLowerCase();
      if (!text) {
        continue;
      }

      if (config.targetCourtKeyword && !text.includes(config.targetCourtKeyword)) {
        continue;
      }

      if (config.targetTimeText && !text.includes(config.targetTimeText.toLowerCase())) {
        continue;
      }

      const action = await findAddToCartAction(candidate);
      if (action) {
        return { candidate, action, text };
      }
    }
  }

  return null;
}

async function findAddToCartAction(candidate) {
  const buttonSelectors = [
    'button:has-text("Add to cart")',
    'button:has-text("Add to Cart")',
    'button:has-text("Book")',
    'button:has-text("Select")',
    '[role="button"]:has-text("Add to cart")',
    '[role="button"]:has-text("Book")'
  ];

  for (const selector of buttonSelectors) {
    const button = candidate.locator(selector).first();
    if (await button.isVisible().catch(() => false)) {
      return button;
    }
  }

  if (await candidate.isVisible().catch(() => false)) {
    return candidate;
  }

  return null;
}

async function addSlotToCart(slot) {
  console.log(`Trying to add slot to cart: ${slot.text}`);
  await slot.action.click({ force: true });
  await slot.action.page().waitForTimeout(1000);
  await dismissCommonPopups(slot.action.page());

  const confirmButtons = [
    /add to cart/i,
    /continue/i,
    /confirm/i,
    /book/i
  ];

  for (const pattern of confirmButtons) {
    const button = slot.action.page().getByRole("button", { name: pattern }).first();
    if (await button.isVisible().catch(() => false)) {
      await button.click().catch(() => null);
      await slot.action.page().waitForTimeout(500);
    }
  }
}

async function tryRefreshAvailability(page) {
  const refreshButton = page.getByRole("button", { name: /refresh|reload|search|update/i }).first();
  if (await refreshButton.isVisible().catch(() => false)) {
    await refreshButton.click().catch(() => null);
    return;
  }

  await page.reload({ waitUntil: "domcontentloaded" }).catch(() => null);
}

async function dismissCommonPopups(page) {
  const buttonNames = [/accept/i, /ok/i, /close/i, /got it/i];
  for (const name of buttonNames) {
    const button = page.getByRole("button", { name }).first();
    if (await button.isVisible().catch(() => false)) {
      await button.click().catch(() => null);
    }
  }
}

function requireEnv(name) {
  const value = (process.env[name] || "").trim();
  if (!value) {
    throw new Error(`Missing required environment variable: ${name}`);
  }
  return value;
}

function loadStorageState() {
  if (fs.existsSync(STORAGE_PATH)) {
    return { storageState: STORAGE_PATH };
  }
  return {};
}

function resolveTargetDate() {
  const explicitDate = (process.env.TARGET_DATE || "").trim();
  if (explicitDate) {
    return explicitDate;
  }

  const offset = parseInteger(process.env.TARGET_OFFSET_DAYS, 0);
  const date = new Date();
  date.setDate(date.getDate() + offset);
  return date.toISOString().slice(0, 10);
}

function parseBoolean(value, fallback) {
  if (value == null || value === "") {
    return fallback;
  }
  return ["1", "true", "yes", "y"].includes(String(value).toLowerCase());
}

function parseInteger(value, fallback) {
  const parsed = Number.parseInt(String(value ?? ""), 10);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function formatDateLabel(dateText) {
  const date = new Date(`${dateText}T00:00:00`);
  return new Intl.DateTimeFormat("en-SG", {
    weekday: "short",
    day: "numeric",
    month: "short",
    year: "numeric"
  }).format(date);
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
