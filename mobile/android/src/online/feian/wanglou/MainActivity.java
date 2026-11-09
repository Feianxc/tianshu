package online.feian.wanglou;

import android.app.Activity;
import android.content.Intent;
import android.content.pm.ApplicationInfo;
import android.graphics.Insets;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.view.View;
import android.view.WindowInsets;
import android.view.WindowInsetsController;
import android.webkit.CookieManager;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.FrameLayout;
import android.window.OnBackInvokedCallback;
import android.window.OnBackInvokedDispatcher;

/**
 * 天枢安卓壳。只做三件事：全屏加载本机天枢、处理返回键、连不上时给出明确提示。
 * 打开构建时指定的天枢地址（build.sh 的 TIANSHU_URL）。前面有 Cloudflare Access 时，
 * 登录页也在壳里打开。插 USB 调试时仍可用本机转发的 127.0.0.1:8787。
 */
public class MainActivity extends Activity {
    private String home;
    private String homeHost;
    static final int BG = 0xFF05070C;
    private WebView web;

    @Override
    protected void onCreate(Bundle state) {
        super.onCreate(state);
        home = getString(R.string.hub_url);
        homeHost = Uri.parse(home).getHost();
        boolean debug = (getApplicationInfo().flags & ApplicationInfo.FLAG_DEBUGGABLE) != 0;
        WebView.setWebContentsDebuggingEnabled(debug);

        FrameLayout root = new FrameLayout(this);
        root.setBackgroundColor(BG);
        web = new WebView(this);
        web.setBackgroundColor(BG);
        web.setOverScrollMode(View.OVER_SCROLL_NEVER);

        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setAllowFileAccess(false);
        s.setAllowContentAccess(false);
        s.setUserAgentString(s.getUserAgentString() + " WanglouShell/0.1");
        CookieManager.getInstance().setAcceptCookie(true);

        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest req) {
                Uri u = req.getUrl();
                String host = u.getHost() == null ? "" : u.getHost();
                if (host.equals(homeHost)) return false;
                // Cloudflare Access 的登录和回调要留在壳里，否则登录状态落到系统浏览器，壳里还是没登录。
                if (host.endsWith(".cloudflareaccess.com")) return false;
                if ("127.0.0.1".equals(host) && u.getPort() == 8787) return false;
                // 天枢以外的链接（GitHub、服务地址）交给系统浏览器，不在壳里打开。
                try {
                    startActivity(new Intent(Intent.ACTION_VIEW, u));
                } catch (Exception ignored) {
                    // 没有可打开的应用就不处理
                }
                return true;
            }

            @Override
            public void onReceivedError(WebView view, WebResourceRequest req, WebResourceError err) {
                if (req.isForMainFrame()) {
                    view.loadDataWithBaseURL(null, offline(String.valueOf(err.getDescription())), "text/html", "utf-8", null);
                }
            }
        });

        root.addView(web, new FrameLayout.LayoutParams(FrameLayout.LayoutParams.MATCH_PARENT, FrameLayout.LayoutParams.MATCH_PARENT));
        setContentView(root);

        // 内容不压到状态栏、导航条、挖孔和键盘下面。
        root.setOnApplyWindowInsetsListener(new View.OnApplyWindowInsetsListener() {
            @Override
            @SuppressWarnings("deprecation")
            public WindowInsets onApplyWindowInsets(View v, WindowInsets insets) {
                if (Build.VERSION.SDK_INT >= 30) {
                    Insets i = insets.getInsets(WindowInsets.Type.systemBars() | WindowInsets.Type.displayCutout() | WindowInsets.Type.ime());
                    v.setPadding(i.left, i.top, i.right, i.bottom);
                    return WindowInsets.CONSUMED;
                }
                v.setPadding(insets.getSystemWindowInsetLeft(), insets.getSystemWindowInsetTop(), insets.getSystemWindowInsetRight(), insets.getSystemWindowInsetBottom());
                return insets.consumeSystemWindowInsets();
            }
        });
        if (Build.VERSION.SDK_INT >= 30) {
            WindowInsetsController c = getWindow().getInsetsController();
            if (c != null) c.setSystemBarsAppearance(0, WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS | WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS);
        }
        if (Build.VERSION.SDK_INT >= 33) {
            getOnBackInvokedDispatcher().registerOnBackInvokedCallback(OnBackInvokedDispatcher.PRIORITY_DEFAULT, new OnBackInvokedCallback() {
                @Override
                public void onBackInvoked() {
                    back();
                }
            });
        }

        if (state != null) web.restoreState(state);
        else web.loadUrl(home);
    }

    private void back() {
        if (web.canGoBack()) web.goBack();
        else finish();
    }

    @Override
    @SuppressWarnings("deprecation")
    public void onBackPressed() {
        back();
    }

    @Override
    protected void onSaveInstanceState(Bundle out) {
        super.onSaveInstanceState(out);
        web.saveState(out);
    }

    @Override
    protected void onPause() {
        super.onPause();
        web.onPause();
        CookieManager.getInstance().flush();
    }

    @Override
    protected void onResume() {
        super.onResume();
        web.onResume();
    }

    @Override
    protected void onDestroy() {
        web.destroy();
        super.onDestroy();
    }

    private String offline(String why) {
        String safe = why.replace("&", "&amp;").replace("<", "&lt;");
        return "<!doctype html><html lang=zh-CN><meta name=viewport content='width=device-width,initial-scale=1'>"
            + "<style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#05070c;color:#e6ebf2;font:15px/1.6 system-ui,sans-serif}"
            + "main{width:min(340px,86vw)}h1{font-size:20px;font-weight:600;margin:0 0 8px}p{color:#a2adbc;margin:0 0 10px;font-size:14px}"
            + "code{color:#7c8799;font-size:12px}button{margin-top:10px;width:100%;min-height:44px;border:0;border-radius:4px;background:#e6ebf2;color:#0a0e14;font:600 15px system-ui}</style>"
            + "<main><h1>连不上天枢</h1><p>检查手机网络，或者确认服务器上的天枢在运行。</p>"
            + "<p><code>" + safe + "</code></p>"
            + "<button onclick=\"location.href='" + home + "'\">重试</button></main>"
            + "<script>setTimeout(function(){location.href='" + home + "'},8000)</script>";
    }
}
