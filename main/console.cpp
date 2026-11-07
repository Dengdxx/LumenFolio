#include "console.h"

#include <cctype>
#include <cstdio>
#include <cstring>

#include "app.h"

namespace {

constexpr const char *kHelp =
    "命令（串口按回车执行，HTTP 用 /console?cmd=<命令>）：\n"
    "  n / next      下一张\n"
    "  p / prev      上一张\n"
    "  r / refresh   重刷当前这张\n"
    "  f / factory   显示出厂画面\n"
    "  s / sleep     立即进入深度睡眠\n"
    "  awake / hold  本次开机不再自动睡眠\n"
    "  status        运行状态与睡眠诊断\n"
    "  help / ?      本帮助\n";

/** @brief 不区分大小写地比较整串。 */
bool same(const char *a, const char *b)
{
    while (*a != '\0' && *b != '\0') {
        if (std::tolower(static_cast<unsigned char>(*a)) !=
            std::tolower(static_cast<unsigned char>(*b))) {
            return false;
        }
        ++a;
        ++b;
    }
    return *a == '\0' && *b == '\0';
}

void reply(char *buf, size_t size, const char *text)
{
    if (buf == nullptr || size == 0) {
        return;
    }
    snprintf(buf, size, "%s", text);
}

/** @brief 投递一条屏幕动作，回执统一写成 "<name> queued"。 */
void post(display_cmd cmd, const char *name, char *buf, size_t size)
{
    const esp_err_t err = app_post_display(cmd);
    if (err == ESP_OK) {
        snprintf(buf, size, "%s queued\n", name);
    } else {
        snprintf(buf, size, "%s failed: %s\n", name, esp_err_to_name(err));
    }
}

/** @brief 去掉首尾空白，返回指向首字符的指针；原地截断尾部。 */
char *trim(char *line)
{
    while (*line != '\0' && std::isspace(static_cast<unsigned char>(*line))) {
        ++line;
    }
    char *end = line + strlen(line);
    while (end > line && std::isspace(static_cast<unsigned char>(end[-1]))) {
        --end;
    }
    *end = '\0';
    return line;
}

}  // namespace

const char *console_help_text()
{
    return kHelp;
}

void console_execute(const char *line, char *buf, size_t size)
{
    if (buf == nullptr || size == 0) {
        return;
    }
    buf[0] = '\0';
    if (line == nullptr) {
        reply(buf, size, "空命令，输入 help 查看用法\n");
        return;
    }

    // console_execute 接受 const 字符串，改到本地副本上再去空白。
    char work[64] = {};
    snprintf(work, sizeof(work), "%s", line);
    const char *cmd = trim(work);

    if (*cmd == '\0') {
        reply(buf, size, "空命令，输入 help 查看用法\n");
    } else if (same(cmd, "help") || same(cmd, "?")) {
        reply(buf, size, kHelp);
    } else if (same(cmd, "status")) {
        app_describe(buf, size);
    } else if (same(cmd, "next") || same(cmd, "n")) {
        post(display_cmd::next, "next", buf, size);
    } else if (same(cmd, "prev") || same(cmd, "p")) {
        post(display_cmd::prev, "prev", buf, size);
    } else if (same(cmd, "refresh") || same(cmd, "r")) {
        post(display_cmd::refresh, "refresh", buf, size);
    } else if (same(cmd, "factory") || same(cmd, "f")) {
        post(display_cmd::factory, "factory", buf, size);
    } else if (same(cmd, "sleep") || same(cmd, "s")) {
        app_request_sleep();
        reply(buf, size, "sleep queued\n");
    } else if (same(cmd, "awake") || same(cmd, "hold")) {
        app_hold_awake();
        reply(buf, size, "本次开机不再自动睡眠\n");
    } else {
        snprintf(buf, size, "未知命令：%s。输入 help 查看用法\n", cmd);
    }
}
