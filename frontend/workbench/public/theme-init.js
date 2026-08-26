try {
    var storedTheme = JSON.parse(localStorage.getItem("infinite-canvas:theme_store") || "{}");
    var theme = storedTheme.state && storedTheme.state.theme === "light" ? "light" : "dark";
    document.documentElement.classList.toggle("dark", theme === "dark");
    document.documentElement.style.colorScheme = theme;
} catch (_) {}
