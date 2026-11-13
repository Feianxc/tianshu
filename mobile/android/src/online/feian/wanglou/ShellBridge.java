package online.feian.wanglou;

import android.content.Context;
import android.os.Build;
import android.webkit.JavascriptInterface;

import org.json.JSONObject;

/**
 * 页面和应用之间唯一的接口，暴露给 window.TianshuShell。只在当前页面属于天枢地址时生效，
 * 别的网站即使被打开也调不动它。令牌只进不出：页面能写入，不能读回。
 */
final class ShellBridge {
    interface PageHost {
        String currentHost();
        String homeHost();
        String hubUrl();
    }

    private final Context ctx;
    private final PageHost page;

    ShellBridge(Context ctx, PageHost page) {
        this.ctx = ctx.getApplicationContext();
        this.page = page;
    }

    private boolean trusted() {
        String h = page.currentHost();
        return h != null && h.equals(page.homeHost());
    }

    @JavascriptInterface
    public String info() {
        if (!trusted()) return "{}";
        try {
            JSONObject o = new JSONObject();
            o.put("model", Build.MANUFACTURER + " " + Build.MODEL);
            o.put("android", Build.VERSION.RELEASE);
            o.put("reporting", Reporter.configured(ctx));
            o.put("device", Reporter.device(ctx));
            o.put("last_ok", Reporter.prefs(ctx).getLong("last_ok", 0));
            o.put("last_code", Reporter.prefs(ctx).getInt("last_code", 0));
            o.put("interval_s", Reporter.INTERVAL_S);
            return o.toString();
        } catch (Exception e) {
            return "{}";
        }
    }

    /** 保存采集器令牌，开启定时上报，并立刻发一次。返回空串表示成功，否则是原因。 */
    @JavascriptInterface
    public String enableReporting(String token, String device) {
        if (!trusted()) return "页面不可信";
        if (token == null || !token.startsWith("wl_c_") || device == null || !device.matches("[a-z0-9][a-z0-9._-]{0,80}")) {
            return "参数不合法";
        }
        Reporter.configure(ctx, token, device);
        String err = Reporter.send(ctx, page.hubUrl());
        return err == null ? "" : err;
    }

    @JavascriptInterface
    public String sendNow() {
        if (!trusted()) return "页面不可信";
        String err = Reporter.send(ctx, page.hubUrl());
        return err == null ? "" : err;
    }

    @JavascriptInterface
    public void disableReporting() {
        if (trusted()) Reporter.forget(ctx);
    }
}
