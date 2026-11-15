package main

import "testing"

// TestOpacitySteps 守住档位表的几个隐含约定。
// 档位表是菜单和命令分发共用的单一来源，改错了不会编译报错，只会表现成
// "点了菜单没反应"或者"勾错项"，所以用测试兜住。
func TestOpacitySteps(t *testing.T) {
	if len(opacitySteps) == 0 {
		t.Fatal("档位不能为空，否则子菜单里一项都没有")
	}

	prev := 0
	hasDefault := false
	for i, v := range opacitySteps {
		if v < 1 || v > 100 {
			t.Errorf("第 %d 档 %d 超出合法范围 1-100（0 会让文字彻底看不见）", i, v)
		}
		if v <= prev {
			t.Errorf("第 %d 档 %d 没有递增（前一档是 %d）", i, v, prev)
		}
		prev = v
		if v == defaultOpacity {
			hasDefault = true
		}
	}

	if !hasDefault {
		t.Errorf("档位里应包含出厂值 %d，否则用户无法从菜单回到默认观感", defaultOpacity)
	}

	// 档位用 idOpacityBase+i 生成 ID，不能和已有的单命令撞车
	for _, id := range []int{idColor, idFont, idToggleFormat, idToggleLock, idToggleAutostart, idExit, idToggleLayer} {
		if id >= idOpacityBase && id < idOpacityBase+len(opacitySteps) {
			t.Errorf("命令 ID %d 落在不透明度档位区间 [%d,%d) 内，消息会被抢走",
				id, idOpacityBase, idOpacityBase+len(opacitySteps))
		}
	}
}

// 主菜单里子菜单所在的位置（0 起算）：颜色、字体、不透明度。
const testSubMenuPos = 2

// TestBuildTrayMenu 断言菜单的实际结构。
//
// 这些错都只在运行时暴露：MF_POPUP 忘了传、子菜单句柄当命令 ID 传进去、
// 勾选算错，编译期全无提示，点起菜单来只会"没反应"。
func TestBuildTrayMenu(t *testing.T) {
	c := &Clock{cfg: defaultConfig()}
	h := c.buildTrayMenu()
	defer pDestroyMenu.Call(h)

	// 颜色 / 字体 / 不透明度 / 分隔 / 12小时制 / 锁定 / 桌面层 / 自启 / 分隔 / 退出
	const wantItems = 10
	if n, _, _ := pGetMenuItemCount.Call(h); int(n) != wantItems {
		t.Fatalf("主菜单项数 = %d，期望 %d", n, wantItems)
	}

	sub, _, _ := pGetSubMenu.Call(h, testSubMenuPos)
	if sub == 0 {
		t.Fatalf("第 %d 项不是子菜单：MF_POPUP 没挂上，或它被当成了普通命令", testSubMenuPos+1)
	}
	if n, _, _ := pGetMenuItemCount.Call(sub); int(n) != len(opacitySteps) {
		t.Errorf("不透明度子菜单项数 = %d，期望 %d", n, len(opacitySteps))
	}

	checked := 0
	for i := range opacitySteps {
		st, _, _ := pGetMenuState.Call(sub, uintptr(i), MF_BYPOSITION)
		if st == ^uintptr(0) {
			t.Fatalf("GetMenuState 读第 %d 档失败", i)
		}
		got := st&MF_CHECKED != 0
		want := opacitySteps[i] == defaultConfig().Opacity
		if got != want {
			t.Errorf("第 %d 档（%d%%）勾选 = %v，期望 %v", i, opacitySteps[i], got, want)
		}
		if got {
			checked++
		}
	}
	if checked != 1 {
		t.Errorf("应恰好有一档被勾选，实际 %d 档", checked)
	}
}

// TestBuildTrayMenuOffStep 覆盖"配置里的值不在预设档位"的情况：
// 手改 config.json 写成 73 是合法的，此时子菜单一项都不该勾。
func TestBuildTrayMenuOffStep(t *testing.T) {
	cfg := defaultConfig()
	cfg.Opacity = 73
	c := &Clock{cfg: cfg}
	h := c.buildTrayMenu()
	defer pDestroyMenu.Call(h)

	sub, _, _ := pGetSubMenu.Call(h, testSubMenuPos)
	if sub == 0 {
		t.Fatal("找不到不透明度子菜单")
	}
	for i := range opacitySteps {
		st, _, _ := pGetMenuState.Call(sub, uintptr(i), MF_BYPOSITION)
		if st&MF_CHECKED != 0 {
			t.Errorf("当前 73%% 不在档位里，第 %d 档（%d%%）不该被勾选", i, opacitySteps[i])
		}
	}
}

// TestOpacityCommandDispatch 走一遍"点击菜单项 → 改值"的链路。
// hwnd 留空：render 见到 hwnd==0 会立刻返回，不碰 GDI，也不需要有窗口。
func TestOpacityCommandDispatch(t *testing.T) {
	for i, want := range opacitySteps {
		c := &Clock{cfg: defaultConfig()}
		c.onCommand(uintptr(idOpacityBase + i))
		if c.cfg.Opacity != want {
			t.Errorf("点第 %d 档后 Opacity = %d，期望 %d", i, c.cfg.Opacity, want)
		}
	}
}

// 档位区间之外的 ID 不能被误判成不透明度命令。
func TestOpacityCommandIgnoresOtherIDs(t *testing.T) {
	c := &Clock{cfg: defaultConfig()}
	before := c.cfg.Opacity
	for _, id := range []int{idOpacityBase - 1, idOpacityBase + len(opacitySteps), 999} {
		c.onCommand(uintptr(id))
		if c.cfg.Opacity != before {
			t.Errorf("ID %d 被误当成不透明度档位，Opacity 变成了 %d", id, c.cfg.Opacity)
		}
	}
}
