// Copies the non-CSS USWDS assets this report needs: Public Sans fonts (the
// only typeface family left after theme/_uswds-theme.scss turns off
// serif/mono), and only the icon SVGs the compiled CSS actually references
// (usa-alert's icons for the alert types/backgrounds it can render) rather
// than the full ~250-file usa-icons/usa-icons-bg sets USWDS ships.
//
// Must run after `npm run build:css` -- it reads the compiled CSS to know
// which icons are referenced.
const fs = require("fs");
const path = require("path");

const ROOT = path.join(__dirname, "..");
const DIST = path.join(ROOT, "node_modules", "@uswds", "uswds", "dist");
const OUT = path.join(ROOT, "docs", "assets", "uswds");
const CSS_PATH = path.join(OUT, "css", "uswds.css");

function copyFile(src, destDir) {
  fs.mkdirSync(destDir, { recursive: true });
  fs.copyFileSync(src, path.join(destDir, path.basename(src)));
}

// -- Fonts: Public Sans only (theme/_uswds-theme.scss turns off serif/mono) --
const fontsSrc = path.join(DIST, "fonts", "public-sans");
const fontsOut = path.join(OUT, "fonts", "public-sans");
fs.rmSync(path.join(OUT, "fonts"), { recursive: true, force: true });
for (const file of fs.readdirSync(fontsSrc)) {
  if (file.endsWith(".woff2")) copyFile(path.join(fontsSrc, file), fontsOut);
}

// -- Icons: only the ones the compiled CSS actually url()-references --
const css = fs.readFileSync(CSS_PATH, "utf8");
const iconRefs = new Set();
for (const m of css.matchAll(/url\((?:"|')?\.\.\/img\/(usa-icons(?:-bg)?)\/([\w.-]+\.svg)(?:"|')?\)/g)) {
  iconRefs.add(`${m[1]}/${m[2]}`);
}

fs.rmSync(path.join(OUT, "img"), { recursive: true, force: true });
for (const ref of iconRefs) {
  const [dir, file] = ref.split("/");
  copyFile(path.join(DIST, "img", dir, file), path.join(OUT, "img", dir));
}

console.log(`copied ${fs.readdirSync(fontsOut).length} font files, ${iconRefs.size} icon files (referenced: ${[...iconRefs].sort().join(", ")})`);
