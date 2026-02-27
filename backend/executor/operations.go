package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/browserwing/browserwing/pkg/logger"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

// Navigate 导航到指定 URL
func (e *Executor) Navigate(ctx context.Context, url string, opts *NavigateOptions) (*OperationResult, error) {
	logger.Info(ctx, "[Navigate] Starting navigation to %s", url)

	if !e.Browser.IsRunning() {
		err := e.Browser.Start(ctx)
		if err != nil {
			return &OperationResult{
				Success:   false,
				Error:     err.Error(),
				Timestamp: time.Now(),
			}, err
		}
		logger.Info(ctx, "[Navigate] Browser started")
	}
	logger.Info(ctx, "[Navigate] Browser is running")

	if opts == nil {
		opts = &NavigateOptions{
			WaitUntil: "load",
			Timeout:   60 * time.Second, // 增加默认超时到60秒
		}
	}
	logger.Info(ctx, "[Navigate] Using timeout: %v, wait_until: %s", opts.Timeout, opts.WaitUntil)

	// 获取或创建页面
	logger.Info(ctx, "[Navigate] Getting active page...")
	page := e.Browser.GetActivePage()
	
	// 检查 page 是否有效
	needNewPage := false
	if page == nil {
		logger.Info(ctx, "[Navigate] No active page")
		needNewPage = true
	} else {
		// 检查 page 的 session 是否仍然有效
		logger.Info(ctx, "[Navigate] Checking if existing page is still valid...")
		checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer checkCancel()
		
		_, err := page.Context(checkCtx).Eval(`() => document.readyState`)
		if err != nil {
			logger.Warn(ctx, "[Navigate] Existing page session is invalid (error: %v), will create new page", err)
			needNewPage = true
		} else {
			logger.Info(ctx, "[Navigate] Existing page is valid")
		}
	}
	
	if needNewPage {
		logger.Info(ctx, "[Navigate] Creating new page...")
		// 通过 OpenPage 创建新页面（会自动导航）
		// 使用当前实例（传空字符串），norecord=true
		err := e.Browser.OpenPage(url, "", "", true)
		if err != nil {
			logger.Error(ctx, "[Navigate] Failed to open page: %s", err.Error())
			return &OperationResult{
				Success:   false,
				Error:     err.Error(),
				Timestamp: time.Now(),
			}, err
		}
		logger.Info(ctx, "[Navigate] Page opened successfully")

		page = e.Browser.GetActivePage()
		if page == nil {
			logger.Error(ctx, "[Navigate] Failed to get active page after opening")
			return &OperationResult{
				Success:   false,
				Error:     "Failed to get active page",
				Timestamp: time.Now(),
			}, fmt.Errorf("failed to get active page")
		}
		logger.Info(ctx, "[Navigate] Got active page")
	} else {
		logger.Info(ctx, "[Navigate] Using existing page, navigating...")
		// 如果已有活动页面，直接导航
		// 使用独立的 context，避免被之前的 context 取消影响
		navCtx, navCancel := context.WithTimeout(context.Background(), opts.Timeout)
		defer navCancel()
		
		err := page.Context(navCtx).Navigate(url)
		if err != nil {
			logger.Error(ctx, "[Navigate] Failed to navigate to page: %s", err.Error())
			
			// 如果是 session 错误，尝试重新创建 page
			if isSessionError(err) {
				logger.Warn(ctx, "[Navigate] Session error detected, retrying with new page...")
				err := e.Browser.OpenPage(url, "", "", true)
				if err != nil {
					return &OperationResult{
						Success:   false,
						Error:     fmt.Sprintf("Navigation failed and retry failed: %s", err.Error()),
						Timestamp: time.Now(),
					}, err
				}
				page = e.Browser.GetActivePage()
				logger.Info(ctx, "[Navigate] Retry successful with new page")
			} else {
				return &OperationResult{
					Success:   false,
					Error:     err.Error(),
					Timestamp: time.Now(),
				}, err
			}
		} else {
			logger.Info(ctx, "[Navigate] Navigation completed")
		}
	}

	// 等待页面加载 - 使用 panic 恢复机制防止 rod 库内部错误
	logger.Info(ctx, "[Navigate] Waiting for page load (condition: %s)...", opts.WaitUntil)
	// 使用独立的 context 进行等待，避免被取消的 context 影响
	waitCtx, waitCancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer waitCancel()
	
	waitErr := safeWaitForPageLoad(waitCtx, page, opts.WaitUntil)
	if waitErr != nil {
		logger.Warn(ctx, "[Navigate] Wait for page load failed: %v (continuing anyway)", waitErr)
		// 不返回错误,因为页面可能已经部分加载了,继续处理
	} else {
		logger.Info(ctx, "[Navigate] Page load completed")
	}

	logger.Info(ctx, "[Navigate] Successfully navigated to %s", url)

	// 获取页面语义树（带超时控制）
	// 注意：这里同步调用，但用带超时的 context
	var accessibilitySnapshotText string

	logger.Info(ctx, "[Navigate] Starting semantic tree extraction...")
	// 创建一个带超时的 context（10秒超时）
	treeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 直接调用，不使用 goroutine 避免资源竞争
	snapshot, err := e.GetAccessibilitySnapshot(treeCtx)
	if err != nil {
		if err == context.DeadlineExceeded {
			logger.Warn(ctx, "[Navigate] Accessibility snapshot extraction timed out after 10s")
		} else if err != context.Canceled {
			logger.Warn(ctx, "[Navigate] Failed to extract accessibility snapshot: %s", err.Error())
		}
		// 不影响导航成功，只是没有可访问性快照
	} else if snapshot != nil {
		accessibilitySnapshotText = snapshot.SerializeToSimpleText()
		logger.Info(ctx, "[Navigate] Successfully extracted accessibility snapshot with %d elements", len(snapshot.Elements))
	} else {
		logger.Warn(ctx, "[Navigate] Accessibility snapshot is nil")
	}

	result := &OperationResult{
		Success:   true,
		Message:   "Successfully navigated to " + url,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"url": url,
		},
	}

	// 如果获取到可访问性快照，添加到返回结果中
	if accessibilitySnapshotText != "" {
		result.Data["accessibility_snapshot"] = accessibilitySnapshotText
	}

	// 录制模式：记录导航操作
	e.recordOp(OperationRecord{
		Type:      "navigate",
		URL:       url,
		Success:   true,
		Timestamp: time.Now(),
	})

	return result, nil
}

// Click 点击元素
func (e *Executor) Click(ctx context.Context, identifier string, opts *ClickOptions) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		logger.Error(ctx, "Failed to get active page")
		return nil, fmt.Errorf("no active page")
	}

	if opts == nil {
		opts = &ClickOptions{
			WaitVisible: true,
			WaitEnabled: true,
			Timeout:     10 * time.Second,
			Button:      "left",
			ClickCount:  1,
		}
	}

	// 查找元素（带超时）
	elem, err := e.findElementWithTimeout(ctx, page, identifier, opts.Timeout)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Element not found: %s", identifier),
			Timestamp: time.Now(),
		}, err
	}

	// 调试：输出元素的详细信息
	elemHTML, _ := elem.HTML()
	elemText, _ := elem.Text()
	htmlPreview := elemHTML
	if len(htmlPreview) > 150 {
		htmlPreview = htmlPreview[:150] + "..."
	}
	logger.Info(ctx, "[Click] Found element - Text: '%s', HTML: %s", elemText, htmlPreview)

	// 等待元素可见
	if opts.WaitVisible {
		elem = elem.Timeout(opts.Timeout)
		if err := elem.WaitVisible(); err != nil {
			return &OperationResult{
				Success:   false,
				Error:     fmt.Sprintf("Element not visible: %s (timeout after %v)", identifier, opts.Timeout),
				Timestamp: time.Now(),
			}, err
		}
	}

	// 等待元素可用
	if opts.WaitEnabled {
		elem = elem.Timeout(opts.Timeout)
		if err := elem.WaitEnabled(); err != nil {
			return &OperationResult{
				Success:   false,
				Error:     fmt.Sprintf("Element not enabled: %s (timeout after %v)", identifier, opts.Timeout),
				Timestamp: time.Now(),
			}, err
		}
	}

	// 滚动到元素
	if err := elem.ScrollIntoView(); err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to scroll to element: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	// 等待页面稳定（关键！避免滚动期间元素位置变化）
	time.Sleep(300 * time.Millisecond)

	// 策略：对于可能被遮挡的场景，直接使用增强的 JavaScript 点击
	// 这样可以确保事件正确触发，不管元素是否被遮挡
	logger.Info(ctx, "[Click] Attempting to click element using enhanced JavaScript: %s", identifier)
	_, jsErr := elem.Eval(`() => {
		// 先聚焦元素
		try {
			this.focus();
		} catch(e) {
			// 忽略聚焦失败
		}
		
		// 触发完整的鼠标事件序列（更可靠）
		const events = ['mousedown', 'mouseup', 'click'];
		events.forEach(eventType => {
			const event = new MouseEvent(eventType, {
				bubbles: true,
				cancelable: true,
				view: window
			});
			this.dispatchEvent(event);
		});
		
		// 如果是按钮或链接，也触发原生 click
		if (this.tagName === 'BUTTON' || this.tagName === 'A' || this.tagName === 'INPUT') {
			this.click();
		}
	}`)
	
	if jsErr != nil {
		// JavaScript 点击失败，尝试正常点击作为后备
		logger.Warn(ctx, "[Click] Enhanced JS click failed, trying normal click: %s", jsErr.Error())
		
		var button proto.InputMouseButton
		switch opts.Button {
		case "right":
			button = proto.InputMouseButtonRight
		case "middle":
			button = proto.InputMouseButtonMiddle
		default:
			button = proto.InputMouseButtonLeft
		}

		if err := elem.Click(button, 1); err != nil {
			return &OperationResult{
				Success:   false,
				Error:     fmt.Sprintf("Both enhanced JS and normal click failed: %s", err.Error()),
				Timestamp: time.Now(),
			}, err
		}
		logger.Info(ctx, "[Click] Normal click succeeded")
	} else {
		logger.Info(ctx, "[Click] ✓ Enhanced JavaScript click succeeded: %s", identifier)
	}

	// 录制模式：记录点击操作
	e.recordOp(OperationRecord{
		Type:          "click",
		Identifier:    identifier,
		ResolvedXPath: getElementXPath(elem),
		Success:       true,
		Timestamp:     time.Now(),
		ElementInfo:   getElementInfo(elem),
	})

	// 同时返回当前的页面可访问性快照
	snapshot, err := e.GetAccessibilitySnapshot(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to get accessibility snapshot: %s", err.Error())
	}
	var accessibilitySnapshotText string
	if snapshot != nil {
		accessibilitySnapshotText = snapshot.SerializeToSimpleText()
	}

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Successfully clicked element: %s", identifier),
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"semantic_tree": accessibilitySnapshotText,
		},
	}, nil
}

// Type 在元素中输入文本
func (e *Executor) Type(ctx context.Context, identifier string, text string, opts *TypeOptions) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	if opts == nil {
		opts = &TypeOptions{
			Clear:       true,
			WaitVisible: true,
			Timeout:     10 * time.Second,
			Delay:       0,
		}
	}

	// 查找元素（带超时）
	elem, err := e.findElementWithTimeout(ctx, page, identifier, opts.Timeout)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Element not found: %s", identifier),
			Timestamp: time.Now(),
		}, err
	}

	// 等待元素可见
	if opts.WaitVisible {
		elem = elem.Timeout(opts.Timeout)
		if err := elem.WaitVisible(); err != nil {
			return &OperationResult{
				Success:   false,
				Error:     fmt.Sprintf("Element not visible: %s (timeout after %v)", identifier, opts.Timeout),
				Timestamp: time.Now(),
			}, err
		}
	}

	// 聚焦元素
	if err := elem.Focus(); err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to focus element: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	// 清空现有内容
	if opts.Clear {
		if err := elem.SelectAllText(); err == nil {
			page.Keyboard.Press(input.Backspace)
		}
	}

	// 输入文本
	if opts.Delay > 0 {
		// 逐字符输入
		for _, char := range text {
			if err := elem.Input(string(char)); err != nil {
				return &OperationResult{
					Success:   false,
					Error:     fmt.Sprintf("Failed to input text: %s", err.Error()),
					Timestamp: time.Now(),
				}, err
			}
			time.Sleep(opts.Delay)
		}
	} else {
		// 一次性输入
		if err := elem.Input(text); err != nil {
			return &OperationResult{
				Success:   false,
				Error:     fmt.Sprintf("Failed to input text: %s", err.Error()),
				Timestamp: time.Now(),
			}, err
		}
	}

	// 录制模式：记录输入操作
	e.recordOp(OperationRecord{
		Type:          "input",
		Identifier:    identifier,
		ResolvedXPath: getElementXPath(elem),
		Value:         text,
		Success:       true,
		Timestamp:     time.Now(),
		ElementInfo:   getElementInfo(elem),
	})

	// 同时返回当前的页面可访问性快照
	snapshot, err := e.GetAccessibilitySnapshot(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to get accessibility snapshot: %s", err.Error())
	}
	var accessibilitySnapshotText string
	if snapshot != nil {
		accessibilitySnapshotText = snapshot.SerializeToSimpleText()
	}

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Successfully typed into element: %s", identifier),
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"text":          text,
			"semantic_tree": accessibilitySnapshotText,
		},
	}, nil
}

// Select 选择下拉框选项
func (e *Executor) Select(ctx context.Context, identifier string, value string, opts *SelectOptions) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	if opts == nil {
		opts = &SelectOptions{
			WaitVisible: true,
			Timeout:     10 * time.Second,
		}
	}

	// 查找元素（带超时）
	elem, err := e.findElementWithTimeout(ctx, page, identifier, opts.Timeout)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Element not found: %s", identifier),
			Timestamp: time.Now(),
		}, err
	}

	// 等待元素可见
	if opts.WaitVisible {
		elem = elem.Timeout(opts.Timeout)
		if err := elem.WaitVisible(); err != nil {
			return &OperationResult{
				Success:   false,
				Error:     fmt.Sprintf("Element not visible: %s (timeout after %v)", identifier, opts.Timeout),
				Timestamp: time.Now(),
			}, err
		}
	}

	// 选择选项
	if err := elem.Select([]string{value}, true, rod.SelectorTypeText); err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to select option: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	// 录制模式：记录选择操作
	e.recordOp(OperationRecord{
		Type:          "select",
		Identifier:    identifier,
		ResolvedXPath: getElementXPath(elem),
		Value:         value,
		Success:       true,
		Timestamp:     time.Now(),
		ElementInfo:   getElementInfo(elem),
	})

	// 同时返回当前的页面可访问性快照
	snapshot, err := e.GetAccessibilitySnapshot(ctx)
	if err != nil {
		logger.Error(ctx, "Failed to get accessibility snapshot: %s", err.Error())
	}
	var accessibilitySnapshotText string
	if snapshot != nil {
		accessibilitySnapshotText = snapshot.SerializeToSimpleText()
	}

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Successfully selected option: %s", value),
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"value":         value,
			"semantic_tree": accessibilitySnapshotText,
		},
	}, nil
}

// GetText 获取元素文本
func (e *Executor) GetText(ctx context.Context, identifier string) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	// 使用默认10秒超时
	elem, err := e.findElementWithTimeout(ctx, page, identifier, 10*time.Second)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Element not found: %s", identifier),
			Timestamp: time.Now(),
		}, err
	}

	text, err := elem.Text()
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get text: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	return &OperationResult{
		Success:   true,
		Message:   "Successfully retrieved text",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"text": text,
		},
	}, nil
}

// GetValue 获取元素值
func (e *Executor) GetValue(ctx context.Context, identifier string) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	// 使用默认10秒超时
	elem, err := e.findElementWithTimeout(ctx, page, identifier, 10*time.Second)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Element not found: %s", identifier),
			Timestamp: time.Now(),
		}, err
	}

	value, err := elem.Property("value")
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get value: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	return &OperationResult{
		Success:   true,
		Message:   "Successfully retrieved value",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"value": value.String(),
		},
	}, nil
}

// WaitFor 等待元素
func (e *Executor) WaitFor(ctx context.Context, identifier string, opts *WaitForOptions) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	if opts == nil {
		opts = &WaitForOptions{
			Timeout: 30 * time.Second,
			State:   "visible",
		}
	}

	// 查找元素（带超时）
	elem, err := e.findElementWithTimeout(ctx, page, identifier, opts.Timeout)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Element not found: %s", identifier),
			Timestamp: time.Now(),
		}, err
	}

	elem = elem.Timeout(opts.Timeout)

	switch opts.State {
	case "visible":
		err = elem.WaitVisible()
	case "hidden":
		err = elem.WaitInvisible()
	case "enabled":
		err = elem.WaitEnabled()
	default:
		err = elem.WaitLoad()
	}

	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Wait failed for state '%s': %s (timeout after %v)", opts.State, err.Error(), opts.Timeout),
			Timestamp: time.Now(),
		}, err
	}

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Successfully waited for element: %s", identifier),
		Timestamp: time.Now(),
	}, nil
}

// Extract 提取数据
func (e *Executor) Extract(ctx context.Context, opts *ExtractOptions) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	if opts == nil {
		return &OperationResult{
			Success:   false,
			Error:     "Extract options required",
			Timestamp: time.Now(),
		}, fmt.Errorf("extract options required")
	}

	var result interface{}

	if opts.Multiple {
		// 提取多个元素
		elements, err := page.Elements(opts.Selector)
		if err != nil {
			return &OperationResult{
				Success:   false,
				Error:     fmt.Sprintf("Failed to find elements: %s", err.Error()),
				Timestamp: time.Now(),
			}, err
		}

		results := make([]map[string]interface{}, 0, len(elements))
		for _, elem := range elements {
			data, err := e.extractElementData(elem, opts)
			if err != nil {
				continue
			}
			results = append(results, data)
		}
		result = results
	} else {
		// 提取单个元素
		elem, err := page.Element(opts.Selector)
		if err != nil {
			return &OperationResult{
				Success:   false,
				Error:     fmt.Sprintf("Failed to find element: %s", err.Error()),
				Timestamp: time.Now(),
			}, err
		}

		data, err := e.extractElementData(elem, opts)
		if err != nil {
			return &OperationResult{
				Success:   false,
				Error:     fmt.Sprintf("Failed to extract data: %s", err.Error()),
				Timestamp: time.Now(),
			}, err
		}
		result = data
	}

	return &OperationResult{
		Success:   true,
		Message:   "Successfully extracted data",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"result": result,
		},
	}, nil
}

// extractElementData 提取元素数据
func (e *Executor) extractElementData(elem *rod.Element, opts *ExtractOptions) (map[string]interface{}, error) {
	data := make(map[string]interface{})

	switch opts.Type {
	case "text":
		text, err := elem.Text()
		if err != nil {
			return nil, err
		}
		data["text"] = text

	case "html":
		html, err := elem.HTML()
		if err != nil {
			return nil, err
		}
		data["html"] = html

	case "attribute":
		if opts.Attr != "" {
			attr, err := elem.Attribute(opts.Attr)
			if err != nil || attr == nil {
				return nil, err
			}
			data[opts.Attr] = *attr
		}

	case "property":
		if opts.Attr != "" {
			prop, err := elem.Property(opts.Attr)
			if err != nil {
				return nil, err
			}
			data[opts.Attr] = prop.String()
		}

	default:
		// 提取指定字段
		if len(opts.Fields) > 0 {
			for _, field := range opts.Fields {
				switch field {
				case "text":
					if text, err := elem.Text(); err == nil {
						data["text"] = text
					}
				case "html":
					if html, err := elem.HTML(); err == nil {
						data["html"] = html
					}
				case "value":
					if val, err := elem.Property("value"); err == nil {
						data["value"] = val.String()
					}
				case "href":
					if href, err := elem.Attribute("href"); err == nil && href != nil {
						data["href"] = *href
					}
				case "src":
					if src, err := elem.Attribute("src"); err == nil && src != nil {
						data["src"] = *src
					}
				}
			}
		} else {
			// 默认提取文本
			text, err := elem.Text()
			if err != nil {
				return nil, err
			}
			data["text"] = text
		}
	}

	return data, nil
}

// findElement 查找元素（支持多种方式），带超时支持
func (e *Executor) findElement(ctx context.Context, page *rod.Page, identifier string) (*rod.Element, error) {
	return e.findElementWithTimeout(ctx, page, identifier, 10*time.Second)
}

// findElementWithTimeout 查找元素（支持多种方式），带自定义超时
func (e *Executor) findElementWithTimeout(ctx context.Context, page *rod.Page, identifier string, timeout time.Duration) (*rod.Element, error) {
	// 设置超时
	timeoutPage := page.Timeout(timeout)

	// 清理 identifier：去除可能的前缀
	identifier = strings.TrimSpace(identifier)
	// 支持 "xpath: //*[@id=...]" 格式
	if strings.HasPrefix(strings.ToLower(identifier), "xpath:") {
		identifier = strings.TrimSpace(identifier[6:]) // 去除 "xpath:" 前缀
		logger.Info(ctx, "[findElementWithTimeout] Detected 'xpath:' prefix, cleaned to: %s", identifier)
	}
	// 支持 "css: #selector" 格式
	if strings.HasPrefix(strings.ToLower(identifier), "css:") {
		identifier = strings.TrimSpace(identifier[4:]) // 去除 "css:" 前缀
		logger.Info(ctx, "[findElementWithTimeout] Detected 'css:' prefix, cleaned to: %s", identifier)
	}

	// 0. 尝试 RefID 格式：@e1, @e2, e1, e2（优先级最高，最稳定）
	if strings.HasPrefix(identifier, "@") || (len(identifier) > 0 && identifier[0] == 'e' && len(identifier) <= 10) {
		refID := strings.TrimPrefix(identifier, "@")
		if elem, err := e.findElementByRefID(ctx, page, refID); err == nil && elem != nil {
			return elem, nil
		}
	}

	// 1. 尝试作为 CSS 选择器
	if elem, err := timeoutPage.Element(identifier); err == nil {
		return elem, nil
	}

	// 2. 尝试作为 XPath
	// 如果是 XPath，尝试找到所有匹配的元素，然后选择最上层可交互的
	if strings.HasPrefix(identifier, "/") || strings.HasPrefix(identifier, "(") {
		elems, err := timeoutPage.ElementsX(identifier)
		if err == nil && len(elems) > 0 {
			// 如果只有一个元素，直接返回
			if len(elems) == 1 {
				return elems[0], nil
			}
			
			// 多个元素时，找到第一个可交互的（未被遮挡的）
			logger.Info(ctx, "[findElementWithTimeout] Found %d elements matching XPath, selecting the interactable one", len(elems))
			for i, elem := range elems {
				// 检查元素是否可见
				visible, _ := elem.Visible()
				if !visible {
					continue
				}
				
				// 检查元素是否可交互（未被遮挡）
				point, err := elem.Interactable()
				if err == nil && point != nil {
					logger.Info(ctx, "[findElementWithTimeout] Selected element #%d (interactable)", i+1)
					return elem, nil
				}
			}
			
			// 如果没有找到可交互的，返回第一个可见的
			for i, elem := range elems {
				visible, _ := elem.Visible()
				if visible {
					logger.Warn(ctx, "[findElementWithTimeout] No interactable element found, using first visible one (#%d)", i+1)
					return elem, nil
				}
			}
			
			// 如果都不可见，返回第一个
			logger.Warn(ctx, "[findElementWithTimeout] No visible element found, using first match")
			return elems[0], nil
		}
	}
	
	// 如果上面的逻辑没有返回，尝试单元素查找
	if elem, err := timeoutPage.ElementX(identifier); err == nil {
		return elem, nil
	}

	// 3. 尝试通过文本查找
	if elem, err := timeoutPage.ElementR("button", identifier); err == nil {
		return elem, nil
	}
	if elem, err := timeoutPage.ElementR("a", identifier); err == nil {
		return elem, nil
	}

	// 4. 尝试通过 aria-label 查找
	selector := fmt.Sprintf("[aria-label*='%s']", identifier)
	if elem, err := timeoutPage.Element(selector); err == nil {
		return elem, nil
	}

	// 5. 尝试通过 placeholder 查找
	selector = fmt.Sprintf("[placeholder*='%s']", identifier)
	if elem, err := timeoutPage.Element(selector); err == nil {
		return elem, nil
	}

	return nil, fmt.Errorf("element not found: %s (timeout after %v)", identifier, timeout)
}

// findElementByRefID 通过 RefID 查找元素（如 e1, e2, e3）
// 混合策略：优先使用 BackendNodeID（快速），失败时使用语义化定位器
func (e *Executor) findElementByRefID(ctx context.Context, page *rod.Page, refID string) (*rod.Element, error) {
	logger.Info(ctx, "[findElementByRefID] Looking up refID: %s", refID)
	
	// 查找 RefID 对应的定位器数据
	e.refIDMutex.RLock()
	refData, found := e.refIDMap[refID]
	cacheAge := time.Since(e.refIDTimestamp)
	e.refIDMutex.RUnlock()
	
	if !found {
		logger.Warn(ctx, "[findElementByRefID] RefID %s not found in cache (age: %v)", refID, cacheAge)
		return nil, fmt.Errorf("refID %s not found (cache may be stale, run browser_snapshot first)", refID)
	}
	
	logger.Info(ctx, "[findElementByRefID] Found refData for %s: role=%s, name=%s, backendID=%d, href=%s (cache age: %v)", 
		refID, refData.Role, refData.Name, refData.BackendID, refData.Href, cacheAge)
	
	// 策略 1：尝试使用 BackendNodeID（最快最准确）
	if refData.BackendID != 0 {
		elem, err := e.findByBackendNodeID(ctx, page, refData.BackendID)
		if err == nil {
			// 验证元素是否匹配（防止DOM变化）
			if e.validateElement(ctx, elem, refData) {
				logger.Info(ctx, "[findElementByRefID] Found element via BackendNodeID for %s", refID)
				return elem, nil
			}
			logger.Warn(ctx, "[findElementByRefID] BackendNodeID element doesn't match, trying semantic locator")
		}
	}
	
	// 策略 2：使用精确属性匹配（href, id, class）
	if refData.Href != "" || (refData.Attributes != nil && refData.Attributes["id"] != "") {
		elem, err := e.findByAttributes(ctx, page, refData)
		if err == nil {
			logger.Info(ctx, "[findElementByRefID] Found element via attributes for %s", refID)
			return elem, nil
		}
		logger.Warn(ctx, "[findElementByRefID] Attribute-based search failed: %v", err)
	}
	
	// 策略 3：使用 role + name + nth（fallback）
	xpath := buildXPathFromRole(refData.Role, refData.Name)
	logger.Info(ctx, "[findElementByRefID] Built XPath: %s", xpath)
	
	elements, err := page.ElementsX(xpath)
	if err != nil {
		logger.Error(ctx, "[findElementByRefID] XPath query failed: %v", err)
		return nil, fmt.Errorf("failed to find elements for refID %s: %w", refID, err)
	}
	
	// 策略 4：通用文本查找（最后的fallback）
	if len(elements) == 0 && refData.Name != "" {
		logger.Warn(ctx, "[findElementByRefID] Role-based XPath found no elements, trying fallback text search")
		
		searchText := refData.Name
		if len(searchText) > 30 {
			searchText = searchText[:30]
		}
		
		fallbackXPath := fmt.Sprintf(`//*[contains(normalize-space(.), '%s') and (
			self::a or self::button or 
			@role='button' or @role='link' or @role='menuitem' or 
			contains(@class, 'btn') or contains(@class, 'link') or contains(@class, 'click') or
			@onclick or @cursor='pointer'
		)]`, searchText)
		
		logger.Info(ctx, "[findElementByRefID] Fallback XPath: %s", fallbackXPath)
		elements, err = page.ElementsX(fallbackXPath)
		if err != nil {
			logger.Error(ctx, "[findElementByRefID] Fallback XPath query failed: %v", err)
		}
	}
	
	if len(elements) == 0 {
		logger.Warn(ctx, "[findElementByRefID] No elements found for refID %s (role=%s, name=%s) even after fallback", 
			refID, refData.Role, refData.Name)
		return nil, fmt.Errorf("element not found for refID %s (page may have changed, run browser_snapshot again)", refID)
	}
	
	logger.Info(ctx, "[findElementByRefID] Found %d matching elements, selecting nth=%d", len(elements), refData.Nth)
	
	// 选择第 nth 个匹配的元素
	if refData.Nth >= len(elements) {
		logger.Error(ctx, "[findElementByRefID] nth=%d out of range (only found %d elements)", refData.Nth, len(elements))
		return nil, fmt.Errorf("refID %s: nth=%d out of range (found %d elements)", refID, refData.Nth, len(elements))
	}
	
	elem := elements[refData.Nth]
	logger.Info(ctx, "[findElementByRefID] Successfully selected element at nth=%d for refID %s", refData.Nth, refID)
	
	// 检查是否是 Text 节点，如果是，返回其父元素
	nodeType, err := elem.Eval(`() => this.nodeType`)
	if err == nil && nodeType != nil && nodeType.Value.Int() == 3 {
		logger.Info(ctx, "[findElementByRefID] RefID %s points to Text node, getting parent", refID)
		parentResult, err := elem.Eval(`() => { return this.parentElement; }`)
		if err != nil {
			return nil, fmt.Errorf("text node has no parent element: %w", err)
		}
		if parentResult == nil {
			return nil, fmt.Errorf("text node parent is null")
		}
		
		parentObj := &proto.RuntimeRemoteObject{
			Type:     "object",
			Subtype:  "node",
			ObjectID: proto.RuntimeRemoteObjectID(parentResult.ObjectID),
		}
		elem, err = page.ElementFromObject(parentObj)
		if err != nil {
			return nil, fmt.Errorf("failed to create element from parent: %w", err)
		}
		logger.Info(ctx, "[findElementByRefID] Successfully got parent element for Text node")
	}
	
	return elem, nil
}

// findByBackendNodeID 通过 BackendNodeID 查找元素
func (e *Executor) findByBackendNodeID(ctx context.Context, page *rod.Page, backendID int) (*rod.Element, error) {
	obj, err := proto.DOMResolveNode{
		BackendNodeID: proto.DOMBackendNodeID(backendID),
	}.Call(page)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve BackendNodeID %d: %w", backendID, err)
	}
	
	if obj.Object.ObjectID == "" {
		return nil, fmt.Errorf("BackendNodeID %d has no ObjectID", backendID)
	}
	
	elem, err := page.ElementFromObject(obj.Object)
	if err != nil {
		return nil, fmt.Errorf("failed to create element from BackendNodeID: %w", err)
	}
	
	return elem, nil
}

// findByAttributes 通过精确属性匹配查找元素
func (e *Executor) findByAttributes(ctx context.Context, page *rod.Page, refData *RefData) (*rod.Element, error) {
	var xpath string
	
	// 优先使用 href（对于链接最精确）
	if refData.Href != "" {
		xpath = fmt.Sprintf("//a[@href='%s']", refData.Href)
		logger.Info(ctx, "[findByAttributes] Using href: %s", xpath)
	} else if id, ok := refData.Attributes["id"]; ok && id != "" {
		// 其次使用 id
		xpath = fmt.Sprintf("//*[@id='%s']", id)
		logger.Info(ctx, "[findByAttributes] Using id: %s", xpath)
	} else {
		return nil, fmt.Errorf("no unique attributes available")
	}
	
	elem, err := page.ElementX(xpath)
	if err != nil {
		return nil, fmt.Errorf("failed to find element by attributes: %w", err)
	}
	
	return elem, nil
}

// validateElement 验证元素是否匹配预期
func (e *Executor) validateElement(ctx context.Context, elem *rod.Element, refData *RefData) bool {
	// 对于链接，验证 href
	if refData.Href != "" {
		href, err := elem.Attribute("href")
		if err == nil && href != nil && *href == refData.Href {
			return true
		}
		logger.Warn(ctx, "[validateElement] href mismatch: expected=%s, got=%v", refData.Href, href)
		return false
	}
	
	// 验证文本内容
	if refData.Name != "" {
		text, err := elem.Text()
		if err == nil && strings.Contains(text, refData.Name[:min(len(refData.Name), 20)]) {
			return true
		}
	}
	
	return true // 默认通过
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}


// Hover 鼠标悬停
func (e *Executor) Hover(ctx context.Context, identifier string, opts *HoverOptions) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	if opts == nil {
		opts = &HoverOptions{
			WaitVisible: true,
			Timeout:     10 * time.Second,
		}
	}

	// 查找元素
	elem, err := e.findElementWithTimeout(ctx, page, identifier, opts.Timeout)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Element not found: %s", identifier),
			Timestamp: time.Now(),
		}, err
	}

	// 等待元素可见
	if opts.WaitVisible {
		elem = elem.Timeout(opts.Timeout)
		if err := elem.WaitVisible(); err != nil {
			return &OperationResult{
				Success:   false,
				Error:     fmt.Sprintf("Element not visible: %s", identifier),
				Timestamp: time.Now(),
			}, err
		}
	}

	// 悬停
	if err := elem.Hover(); err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to hover: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Successfully hovered over element: %s", identifier),
		Timestamp: time.Now(),
	}, nil
}

// ScrollToBottom 滚动到页面底部
func (e *Executor) ScrollToBottom(ctx context.Context) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	// 使用安全的 Eval 包装,防止 panic
	err := safeScrollEval(ctx, page)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to scroll: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	e.recordOp(OperationRecord{
		Type:      "scroll",
		Success:   true,
		Timestamp: time.Now(),
	})

	return &OperationResult{
		Success:   true,
		Message:   "Successfully scrolled to bottom",
		Timestamp: time.Now(),
	}, nil
}

// GoBack 后退
func (e *Executor) GoBack(ctx context.Context) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	if err := page.NavigateBack(); err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to go back: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	e.recordOp(OperationRecord{
		Type:      "go_back",
		Success:   true,
		Timestamp: time.Now(),
	})

	return &OperationResult{
		Success:   true,
		Message:   "Successfully navigated back",
		Timestamp: time.Now(),
	}, nil
}

// GoForward 前进
func (e *Executor) GoForward(ctx context.Context) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	if err := page.NavigateForward(); err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to go forward: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	e.recordOp(OperationRecord{
		Type:      "go_forward",
		Success:   true,
		Timestamp: time.Now(),
	})

	return &OperationResult{
		Success:   true,
		Message:   "Successfully navigated forward",
		Timestamp: time.Now(),
	}, nil
}

// Reload 刷新页面
func (e *Executor) Reload(ctx context.Context) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	if err := page.Reload(); err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to reload: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	e.recordOp(OperationRecord{
		Type:      "reload",
		Success:   true,
		Timestamp: time.Now(),
	})

	return &OperationResult{
		Success:   true,
		Message:   "Successfully reloaded page",
		Timestamp: time.Now(),
	}, nil
}

// Screenshot 截图
func (e *Executor) Screenshot(ctx context.Context, opts *ScreenshotOptions) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	if opts == nil {
		opts = &ScreenshotOptions{
			FullPage: false,
			Quality:  80,
			Format:   "png",
		}
	}

	var format proto.PageCaptureScreenshotFormat
	if opts.Format == "jpeg" || opts.Format == "jpg" {
		format = proto.PageCaptureScreenshotFormatJpeg
	} else {
		format = proto.PageCaptureScreenshotFormatPng
	}

	var data []byte
	var err error

	if opts.FullPage {
		data, err = page.Screenshot(opts.FullPage, &proto.PageCaptureScreenshot{
			Format:  format,
			Quality: &opts.Quality,
		})
	} else {
		data, err = page.Screenshot(false, &proto.PageCaptureScreenshot{
			Format:  format,
			Quality: &opts.Quality,
		})
	}

	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to take screenshot: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	// 保存截图到文件
	screenshotPath, saveErr := e.saveScreenshot(ctx, data, opts.Format)
	if saveErr != nil {
		logger.Warn(ctx, "Failed to save screenshot to file: %v", saveErr)
	}

	resultData := map[string]interface{}{
		"data":   data,
		"format": opts.Format,
		"size":   len(data),
	}
	
	// 如果保存成功，添加路径信息
	if screenshotPath != "" {
		resultData["path"] = screenshotPath
	}

	message := fmt.Sprintf("Successfully captured screenshot (%d bytes)", len(data))
	if screenshotPath != "" {
		message = fmt.Sprintf("Successfully captured screenshot (%d bytes) and saved to: %s", len(data), screenshotPath)
	}

	return &OperationResult{
		Success:   true,
		Message:   message,
		Timestamp: time.Now(),
		Data:      resultData,
	}, nil
}

// Evaluate 执行 JavaScript 代码
func (e *Executor) Evaluate(ctx context.Context, script string) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	// 使用安全的执行方式,防止 rod 库 panic
	var result interface{}
	err := safeEvaluate(ctx, page, script, &result)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to execute script: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	e.recordOp(OperationRecord{
		Type:      "evaluate",
		JSCode:    script,
		Success:   true,
		Timestamp: time.Now(),
	})

	return &OperationResult{
		Success:   true,
		Message:   "Successfully executed script",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"result": result,
		},
	}, nil
}

// PressKey 按键
func (e *Executor) PressKey(ctx context.Context, key string, opts *PressKeyOptions) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	if opts == nil {
		opts = &PressKeyOptions{}
	}

	// 创建 keyboard
	keyboard := page.Keyboard

	// 按下修饰键
	if opts.Ctrl {
		keyboard.Press(input.ControlLeft)
		defer keyboard.Release(input.ControlLeft)
	}
	if opts.Shift {
		keyboard.Press(input.ShiftLeft)
		defer keyboard.Release(input.ShiftLeft)
	}
	if opts.Alt {
		keyboard.Press(input.AltLeft)
		defer keyboard.Release(input.AltLeft)
	}
	if opts.Meta {
		keyboard.Press(input.MetaLeft)
		defer keyboard.Release(input.MetaLeft)
	}

	// 按下并释放目标键
	var keyCode input.Key
	switch strings.ToLower(key) {
	case "enter", "return":
		keyCode = input.Enter
	case "tab":
		keyCode = input.Tab
	case "escape", "esc":
		keyCode = input.Escape
	case "backspace":
		keyCode = input.Backspace
	case "delete":
		keyCode = input.Delete
	case "arrowup", "up":
		keyCode = input.ArrowUp
	case "arrowdown", "down":
		keyCode = input.ArrowDown
	case "arrowleft", "left":
		keyCode = input.ArrowLeft
	case "arrowright", "right":
		keyCode = input.ArrowRight
	case "home":
		keyCode = input.Home
	case "end":
		keyCode = input.End
	case "pageup":
		keyCode = input.PageUp
	case "pagedown":
		keyCode = input.PageDown
	case "space":
		keyCode = input.Space
	default:
		// 单个字符
		if len(key) == 1 {
			err := keyboard.Type(input.Key(key[0]))
			if err != nil {
				return &OperationResult{
					Success:   false,
					Error:     fmt.Sprintf("Failed to press key: %s", err.Error()),
					Timestamp: time.Now(),
				}, err
			}
			e.recordOp(OperationRecord{
				Type:      "keyboard",
				Key:       key,
				Success:   true,
				Timestamp: time.Now(),
			})
			return &OperationResult{
				Success:   true,
				Message:   fmt.Sprintf("Successfully pressed key: %s", key),
				Timestamp: time.Now(),
			}, nil
		}
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Unknown key: %s", key),
			Timestamp: time.Now(),
		}, fmt.Errorf("unknown key: %s", key)
	}

	if err := keyboard.Press(keyCode); err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to press key: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}
	keyboard.Release(keyCode)

	e.recordOp(OperationRecord{
		Type:      "keyboard",
		Key:       key,
		Success:   true,
		Timestamp: time.Now(),
	})

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Successfully pressed key: %s", key),
		Timestamp: time.Now(),
	}, nil
}

// Resize 调整浏览器窗口大小
func (e *Executor) Resize(ctx context.Context, width, height int) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width:  width,
		Height: height,
	})
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to resize window: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Successfully resized window to %dx%d", width, height),
		Timestamp: time.Now(),
	}, nil
}

// GetConsoleMessages 获取控制台消息
func (e *Executor) GetConsoleMessages(ctx context.Context) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	// 收集控制台消息
	messages := []map[string]interface{}{}

	// 监听控制台事件
	go page.EachEvent(func(e *proto.RuntimeConsoleAPICalled) {
		msg := map[string]interface{}{
			"type":      e.Type,
			"timestamp": time.Now().Format(time.RFC3339),
			"args":      []string{},
		}
		for _, arg := range e.Args {
			if arg.Value.Val() != nil {
				msg["args"] = append(msg["args"].([]string), fmt.Sprintf("%v", arg.Value.Val()))
			}
		}
		messages = append(messages, msg)
	})()

	// 等待一小段时间以收集消息
	time.Sleep(100 * time.Millisecond)

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Retrieved %d console messages", len(messages)),
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"messages": messages,
		},
	}, nil
}

// HandleDialog 处理对话框（alert, confirm, prompt）
func (e *Executor) HandleDialog(ctx context.Context, accept bool, text string) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	// 设置对话框处理器
	go page.EachEvent(func(e *proto.PageJavascriptDialogOpening) {
		if accept {
			proto.PageHandleJavaScriptDialog{
				Accept:     true,
				PromptText: text,
			}.Call(page)
		} else {
			proto.PageHandleJavaScriptDialog{
				Accept: false,
			}.Call(page)
		}
	})()

	return &OperationResult{
		Success:   true,
		Message:   "Dialog handler configured",
		Timestamp: time.Now(),
	}, nil
}

// FileUpload 上传文件
func (e *Executor) FileUpload(ctx context.Context, identifier string, filePaths []string) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	elem, err := e.findElementWithTimeout(ctx, page, identifier, 10*time.Second)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to find element: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	err = elem.SetFiles(filePaths)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to upload files: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Successfully uploaded %d file(s)", len(filePaths)),
		Timestamp: time.Now(),
	}, nil
}

// Drag 拖拽元素
func (e *Executor) Drag(ctx context.Context, fromIdentifier, toIdentifier string) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	fromElem, err := e.findElementWithTimeout(ctx, page, fromIdentifier, 10*time.Second)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to find source element: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	toElem, err := e.findElementWithTimeout(ctx, page, toIdentifier, 10*time.Second)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to find target element: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	// 获取源元素和目标元素的位置
	fromBox, err := fromElem.Shape()
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get source element shape: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	toBox, err := toElem.Shape()
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get target element shape: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	// 计算中心点
	fromRect := fromBox.Box()
	toRect := toBox.Box()
	fromCenter := proto.Point{
		X: fromRect.X + fromRect.Width/2,
		Y: fromRect.Y + fromRect.Height/2,
	}
	toCenter := proto.Point{
		X: toRect.X + toRect.Width/2,
		Y: toRect.Y + toRect.Height/2,
	}

	// 执行拖拽
	err = page.Mouse.MoveLinear(fromCenter, 10)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to move to source: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	err = page.Mouse.Down(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to mouse down: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	err = page.Mouse.MoveLinear(toCenter, 10)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to move to target: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	err = page.Mouse.Up(proto.InputMouseButtonLeft, 1)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to mouse up: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	return &OperationResult{
		Success:   true,
		Message:   "Successfully dragged element",
		Timestamp: time.Now(),
	}, nil
}

// ClosePage 关闭当前页面
func (e *Executor) ClosePage(ctx context.Context) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	err := page.Close()
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to close page: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	return &OperationResult{
		Success:   true,
		Message:   "Successfully closed page",
		Timestamp: time.Now(),
	}, nil
}

// GetNetworkRequests 获取网络请求（需要先启用网络监控）
func (e *Executor) GetNetworkRequests(ctx context.Context) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	// 启用网络监控
	err := proto.NetworkEnable{}.Call(page)
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to enable network monitoring: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	requests := []map[string]interface{}{}

	// 监听网络请求
	go page.EachEvent(func(e *proto.NetworkRequestWillBeSent) {
		req := map[string]interface{}{
			"url":       e.Request.URL,
			"method":    e.Request.Method,
			"timestamp": time.Now().Format(time.RFC3339),
			"type":      e.Type,
		}
		requests = append(requests, req)
	})()

	// 等待一段时间收集请求
	time.Sleep(100 * time.Millisecond)

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Retrieved %d network requests", len(requests)),
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"requests": requests,
		},
	}, nil
}

// isSessionError 检查是否是 CDP session 错误
func isSessionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// CDP session 相关的错误
	return strings.Contains(errStr, "Session with given id not found") ||
		strings.Contains(errStr, "Session closed") ||
		strings.Contains(errStr, "Target closed") ||
		strings.Contains(errStr, "-32001")
}

// safeWaitForPageLoad 安全地等待页面加载,捕获可能的 panic
func safeWaitForPageLoad(ctx context.Context, page *rod.Page, waitUntil string) (err error) {
	// 使用 defer recover 来捕获 rod 库可能产生的 panic
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during page wait: %v", r)
		}
	}()

	// 使用传入的 context 来控制超时
	page = page.Context(ctx)
	
	// 根据不同的等待条件执行相应的等待操作
	switch waitUntil {
	case "domcontentloaded", "load":
		err = page.WaitLoad()
	case "networkidle", "idle":
		page.WaitIdle(2 * time.Second)
	default:
		err = page.WaitLoad()
	}

	return err
}

// safeScrollEval 安全地执行滚动操作,捕获可能的 panic
func safeScrollEval(ctx context.Context, page *rod.Page) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during scroll eval: %v", r)
		}
	}()

	_, err = page.Eval(`() => {
		window.scrollTo(0, document.body.scrollHeight);
	}`)
	return err
}

// safeEvaluate 安全地执行 JavaScript,捕获可能的 panic
func safeEvaluate(ctx context.Context, page *rod.Page, script string, result interface{}) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic during evaluate: %v", r)
		}
	}()

	// 智能包装脚本：如果不是函数格式，自动包装为箭头函数
	wrappedScript := wrapScriptIfNeeded(script)

	evalResult, evalErr := page.Eval(wrappedScript)
	if evalErr != nil {
		return evalErr
	}

	// 尝试解析结果
	if evalResult != nil {
		*result.(*interface{}) = evalResult.Value.Val()
	}

	return nil
}

// wrapScriptIfNeeded 智能包装脚本
// 如果脚本不是函数格式，自动包装为箭头函数
func wrapScriptIfNeeded(script string) string {
	script = strings.TrimSpace(script)
	
	// 检测是否已经是函数格式
	// 1. 箭头函数：() => { ... } 或 () => ...
	// 2. 普通函数：function() { ... }
	// 3. 异步函数：async () => { ... } 或 async function() { ... }
	if strings.HasPrefix(script, "()") ||
		strings.HasPrefix(script, "function") ||
		strings.HasPrefix(script, "async ") {
		return script
	}

	// 不是函数格式，需要包装
	// 包装为箭头函数：() => { 用户代码 }
	return fmt.Sprintf("() => {\n%s\n}", script)
}

// TabsAction 标签页操作类型
type TabsAction string

const (
	TabsActionList   TabsAction = "list"
	TabsActionNew    TabsAction = "new"
	TabsActionSwitch TabsAction = "switch"
	TabsActionClose  TabsAction = "close"
)

// TabsOptions 标签页操作选项
type TabsOptions struct {
	Action TabsAction // 操作类型：list, new, switch, close
	URL    string     // 新建标签页时的 URL（action=new 时必需）
	Index  int        // 标签页索引（action=switch 或 close 时使用，0-based）
}

// TabInfo 标签页信息
type TabInfo struct {
	Index  int    `json:"index"`   // 标签页索引（0-based）
	Title  string `json:"title"`   // 页面标题
	URL    string `json:"url"`     // 页面 URL
	Active bool   `json:"active"`  // 是否为当前活动标签页
	Type   string `json:"type"`    // 标签页类型
}

// Tabs 标签页管理
func (e *Executor) Tabs(ctx context.Context, opts *TabsOptions) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	browser := page.Browser()
	if browser == nil {
		return nil, fmt.Errorf("no browser instance")
	}

	switch opts.Action {
	case TabsActionList:
		return e.listTabs(ctx, browser, page)
	case TabsActionNew:
		if opts.URL == "" {
			return nil, fmt.Errorf("URL is required for new tab action")
		}
		return e.newTab(ctx, browser, opts.URL)
	case TabsActionSwitch:
		return e.switchTab(ctx, browser, opts.Index)
	case TabsActionClose:
		return e.closeTab(ctx, browser, opts.Index)
	default:
		return nil, fmt.Errorf("unknown tabs action: %s", opts.Action)
	}
}

// listTabs 列出所有标签页
func (e *Executor) listTabs(ctx context.Context, browser *rod.Browser, currentPage *rod.Page) (*OperationResult, error) {
	pages, err := browser.Pages()
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get tabs: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	tabs := make([]TabInfo, 0, len(pages))
	for i, p := range pages {
		info, err := p.Info()
		if err != nil {
			logger.Warn(ctx, "Failed to get tab info for index %d: %v", i, err)
			continue
		}

		// 只列出 type="page" 的标签页，排除扩展、devtools 等
		if info.Type != "page" {
			continue
		}

		tab := TabInfo{
			Index:  i,
			Title:  info.Title,
			URL:    info.URL,
			Active: p == currentPage,
			Type:   string(info.Type),
		}
		tabs = append(tabs, tab)
	}

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Found %d tabs", len(tabs)),
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"tabs":  tabs,
			"count": len(tabs),
		},
	}, nil
}

// newTab 创建新标签页
func (e *Executor) newTab(ctx context.Context, browser *rod.Browser, url string) (*OperationResult, error) {
	logger.Info(ctx, "Creating new tab with URL: %s", url)

	newPage, err := browser.Page(proto.TargetCreateTarget{URL: url})
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to create new tab: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	// 等待页面加载
	if err := newPage.WaitLoad(); err != nil {
		logger.Warn(ctx, "New tab loaded but with warning: %v", err)
	}

	// 新标签页自动成为活动标签页
	e.Browser.SetActivePage(newPage)

	// 页面已切换，使 snapshot 缓存失效
	e.InvalidateSnapshotCache()

	// 获取新标签页的信息
	info, err := newPage.Info()
	if err != nil {
		logger.Warn(ctx, "Failed to get new tab info: %v", err)
	}

	// 获取新标签页的索引
	pages, _ := browser.Pages()
	newIndex := -1
	for i, p := range pages {
		if p == newPage {
			newIndex = i
			break
		}
	}

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Successfully created new tab at index %d", newIndex),
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"index": newIndex,
			"url":   url,
			"title": info.Title,
		},
	}, nil
}

// switchTab 切换到指定标签页
func (e *Executor) switchTab(ctx context.Context, browser *rod.Browser, index int) (*OperationResult, error) {
	pages, err := browser.Pages()
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get tabs: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	// 过滤只保留 type="page" 的标签页
	var pageTabs []*rod.Page
	for _, p := range pages {
		info, err := p.Info()
		if err != nil {
			continue
		}
		if info.Type == "page" {
			pageTabs = append(pageTabs, p)
		}
	}

	if index < 0 || index >= len(pageTabs) {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Tab index %d is out of range (0-%d)", index, len(pageTabs)-1),
			Timestamp: time.Now(),
		}, fmt.Errorf("invalid tab index: %d", index)
	}

	targetPage := pageTabs[index]

	// 激活目标标签页
	_, err = targetPage.Activate()
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to activate tab: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	// 更新 Manager 中的 activePage，确保后续操作使用新标签页
	e.Browser.SetActivePage(targetPage)

	// 页面已切换，使 snapshot 缓存失效
	e.InvalidateSnapshotCache()

	// 获取标签页信息
	info, _ := targetPage.Info()

	logger.Info(ctx, "Switched to tab %d: %s", index, info.URL)

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Successfully switched to tab %d", index),
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"index": index,
			"url":   info.URL,
			"title": info.Title,
		},
	}, nil
}

// closeTab 关闭指定标签页
func (e *Executor) closeTab(ctx context.Context, browser *rod.Browser, index int) (*OperationResult, error) {
	pages, err := browser.Pages()
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to get tabs: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	// 过滤只保留 type="page" 的标签页
	var pageTabs []*rod.Page
	for _, p := range pages {
		info, err := p.Info()
		if err != nil {
			continue
		}
		if info.Type == "page" {
			pageTabs = append(pageTabs, p)
		}
	}

	if index < 0 || index >= len(pageTabs) {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Tab index %d is out of range (0-%d)", index, len(pageTabs)-1),
			Timestamp: time.Now(),
		}, fmt.Errorf("invalid tab index: %d", index)
	}

	targetPage := pageTabs[index]
	info, _ := targetPage.Info()

	// 检查是否关闭的是当前活动页面
	currentPage := e.Browser.GetActivePage()
	closingActivePage := (targetPage == currentPage)

	// 关闭标签页
	err = targetPage.Close()
	if err != nil {
		return &OperationResult{
			Success:   false,
			Error:     fmt.Sprintf("Failed to close tab: %s", err.Error()),
			Timestamp: time.Now(),
		}, err
	}

	// 如果关闭的是活动页面，切换到剩余的第一个页面标签
	if closingActivePage {
		remainingPages, _ := browser.Pages()
		for _, p := range remainingPages {
			pInfo, pErr := p.Info()
			if pErr != nil {
				continue
			}
			if pInfo.Type == "page" {
				e.Browser.SetActivePage(p)
				_, _ = p.Activate()
				break
			}
		}
	}

	// 使 snapshot 缓存失效
	e.InvalidateSnapshotCache()

	logger.Info(ctx, "Closed tab %d: %s", index, info.URL)

	return &OperationResult{
		Success:   true,
		Message:   fmt.Sprintf("Successfully closed tab %d", index),
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"index": index,
			"url":   info.URL,
			"title": info.Title,
		},
	}, nil
}

// FormField 表单字段
type FormField struct {
	Name  string      `json:"name"`  // 字段名称（name, id, label, placeholder等）
	Value interface{} `json:"value"` // 字段值（string, bool, []string等）
	Type  string      `json:"type"`  // 字段类型（可选：text, email, password, select, checkbox, radio等）
}

// FillFormOptions 批量填写表单选项
type FillFormOptions struct {
	Fields []FormField   // 要填写的字段列表
	Submit bool          // 是否自动提交表单
	Timeout time.Duration // 超时时间
}

// FillForm 批量填写表单
func (e *Executor) FillForm(ctx context.Context, opts *FillFormOptions) (*OperationResult, error) {
	page := e.Browser.GetActivePage()
	if page == nil {
		return nil, fmt.Errorf("no active page")
	}

	if opts == nil || len(opts.Fields) == 0 {
		return nil, fmt.Errorf("no fields provided")
	}

	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}

	logger.Info(ctx, "Filling form with %d fields", len(opts.Fields))

	filled := 0
	var errors []string

	for i, field := range opts.Fields {
		logger.Info(ctx, "Processing field %d/%d: %s", i+1, len(opts.Fields), field.Name)

		err := e.fillSingleField(ctx, page, field, opts.Timeout)
		if err != nil {
			errMsg := fmt.Sprintf("Field '%s': %s", field.Name, err.Error())
			errors = append(errors, errMsg)
			logger.Warn(ctx, "Failed to fill field '%s': %v", field.Name, err)
		} else {
			filled++
			logger.Info(ctx, "✓ Successfully filled field: %s", field.Name)
		}
	}

	// 如果需要提交表单
	if opts.Submit {
		logger.Info(ctx, "Submitting form...")
		err := e.submitForm(ctx, page)
		if err != nil {
			errors = append(errors, fmt.Sprintf("Submit failed: %s", err.Error()))
		} else {
			logger.Info(ctx, "✓ Form submitted successfully")
		}
	}

	// 构建结果消息
	message := fmt.Sprintf("Successfully filled %d/%d fields", filled, len(opts.Fields))
	if opts.Submit {
		if len(errors) == 0 || errors[len(errors)-1] != "Submit failed" {
			message += " and submitted form"
		}
	}

	return &OperationResult{
		Success:   len(errors) == 0 || filled > 0,
		Message:   message,
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"filled_count": filled,
			"total_fields": len(opts.Fields),
			"errors":       errors,
			"submitted":    opts.Submit,
		},
	}, nil
}

// fillSingleField 填写单个表单字段
func (e *Executor) fillSingleField(ctx context.Context, page *rod.Page, field FormField, timeout time.Duration) error {
	// 尝试多种方式查找元素
	selectors := []string{
		fmt.Sprintf("input[name='%s']", field.Name),
		fmt.Sprintf("input[id='%s']", field.Name),
		fmt.Sprintf("textarea[name='%s']", field.Name),
		fmt.Sprintf("textarea[id='%s']", field.Name),
		fmt.Sprintf("select[name='%s']", field.Name),
		fmt.Sprintf("select[id='%s']", field.Name),
		fmt.Sprintf("input[placeholder='%s']", field.Name),
		fmt.Sprintf("input[aria-label='%s']", field.Name),
	}

	var elem *rod.Element
	var err error

	// 尝试每个选择器
	for _, selector := range selectors {
		elem, err = page.Timeout(timeout).Element(selector)
		if err == nil && elem != nil {
			break
		}
	}

	// 如果还是找不到，尝试通过 label 文本查找
	if elem == nil {
		elem, err = e.findElementByLabel(ctx, page, field.Name, timeout)
	}

	if elem == nil || err != nil {
		return fmt.Errorf("element not found with name '%s'", field.Name)
	}

	// 根据元素类型填写值
	tagName, _ := elem.Eval(`() => this.tagName.toLowerCase()`)
	if tagName == nil {
		return fmt.Errorf("failed to get element tag name")
	}

	tag := tagName.Value.String()
	tag = strings.Trim(tag, "\"")

	switch tag {
	case "input":
		return e.fillInputField(ctx, elem, field, timeout)
	case "textarea":
		return e.fillTextareaField(ctx, elem, field)
	case "select":
		return e.fillSelectField(ctx, elem, field)
	default:
		return fmt.Errorf("unsupported element type: %s", tag)
	}
}

// fillInputField 填写 input 字段
func (e *Executor) fillInputField(ctx context.Context, elem *rod.Element, field FormField, timeout time.Duration) error {
	// 获取 input 类型
	inputType, _ := elem.Attribute("type")
	if inputType == nil {
		defaultType := "text"
		inputType = &defaultType
	}

	switch *inputType {
	case "checkbox", "radio":
		// 复选框和单选框
		shouldCheck := false
		switch v := field.Value.(type) {
		case bool:
			shouldCheck = v
		case string:
			shouldCheck = v == "true" || v == "1" || v == "yes" || v == "on"
		}

		isChecked, _ := elem.Property("checked")
		currentlyChecked := false
		if isChecked.Nil() == false {
			currentlyChecked = isChecked.Bool()
		}

		if shouldCheck != currentlyChecked {
			return elem.Click(proto.InputMouseButtonLeft, 1)
		}
		return nil

	default:
		// 文本输入框（text, email, password, url, tel, number 等）
		valueStr := fmt.Sprintf("%v", field.Value)

		// 滚动到元素
		elem.ScrollIntoView()

		// 聚焦元素
		elem.Focus()

		// 清空现有内容
		elem.SelectAllText()
		elem.Input("")

		// 输入新值
		return elem.Input(valueStr)
	}
}

// fillTextareaField 填写 textarea 字段
func (e *Executor) fillTextareaField(ctx context.Context, elem *rod.Element, field FormField) error {
	valueStr := fmt.Sprintf("%v", field.Value)

	elem.ScrollIntoView()
	elem.Focus()
	elem.SelectAllText()
	elem.Input("")

	return elem.Input(valueStr)
}

// fillSelectField 填写 select 下拉框
func (e *Executor) fillSelectField(ctx context.Context, elem *rod.Element, field FormField) error {
	valueStr := fmt.Sprintf("%v", field.Value)

	// 先尝试按显示文本选择
	err := elem.Select([]string{valueStr}, true, rod.SelectorTypeText)
	if err == nil {
		return nil
	}

	// 如果按文本选择失败，尝试使用 JavaScript 按 value 属性设置
	_, err = elem.Eval(fmt.Sprintf(`(elem) => {
		elem.value = '%s';
		elem.dispatchEvent(new Event('change', { bubbles: true }));
	}`, valueStr))

	return err
}

// findElementByLabel 通过 label 文本查找输入元素
func (e *Executor) findElementByLabel(ctx context.Context, page *rod.Page, labelText string, timeout time.Duration) (*rod.Element, error) {
	// 尝试查找包含该文本的 label 元素
	labels, err := page.Timeout(timeout).Elements("label")
	if err != nil {
		return nil, err
	}

	for _, label := range labels {
		text, err := label.Text()
		if err != nil {
			continue
		}

		if strings.Contains(strings.ToLower(text), strings.ToLower(labelText)) {
			// 找到匹配的 label，获取其 for 属性
			forAttr, _ := label.Attribute("for")
			if forAttr != nil && *forAttr != "" {
				// 通过 ID 查找输入元素
				return page.Element(fmt.Sprintf("#%s", *forAttr))
			}

			// 如果没有 for 属性，查找 label 内部的输入元素
			input, err := label.Element("input, textarea, select")
			if err == nil {
				return input, nil
			}
		}
	}

	return nil, fmt.Errorf("no element found with label: %s", labelText)
}

// submitForm 提交表单
func (e *Executor) submitForm(ctx context.Context, page *rod.Page) error {
	// 尝试多种方式提交表单

	// 1. 查找 submit 按钮
	submitSelectors := []string{
		"button[type='submit']",
		"input[type='submit']",
		"button:not([type])",  // button 默认 type 是 submit
		"button",              // 最后尝试任何 button
	}

	for _, selector := range submitSelectors {
		elem, err := page.Element(selector)
		if err == nil && elem != nil {
			// 检查按钮是否可见和可用
			visible, _ := elem.Visible()
			if !visible {
				continue
			}

			// 点击提交按钮
			return elem.Click(proto.InputMouseButtonLeft, 1)
		}
	}

	// 2. 如果找不到提交按钮，尝试在任何输入框按 Enter
	inputs, err := page.Elements("input[type='text'], input[type='email'], input[type='password']")
	if err == nil && len(inputs) > 0 {
		inputs[0].Focus()
		return page.Keyboard.Press(input.Enter)
	}

	return fmt.Errorf("no submit button found")
}

// saveScreenshot 将截图数据保存到文件
func (e *Executor) saveScreenshot(ctx context.Context, data []byte, format string) (string, error) {
	// 创建 screenshots 目录
	screenshotsDir := "screenshots"
	if err := os.MkdirAll(screenshotsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create screenshots directory: %w", err)
	}

	// 生成文件名：screenshot_YYYYMMDD_HHMMSS.{format}
	timestamp := time.Now().Format("20060102_150405")
	extension := format
	if extension == "jpg" {
		extension = "jpeg"
	}
	filename := fmt.Sprintf("screenshot_%s.%s", timestamp, extension)
	filepath := filepath.Join(screenshotsDir, filename)

	// 保存文件
	if err := os.WriteFile(filepath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write screenshot file: %w", err)
	}

	logger.Info(ctx, "Screenshot saved to: %s", filepath)
	return filepath, nil
}
