package menu

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	managerMenu "github.com/limes-cloud/manager/api/menu"
	"google.golang.org/grpc/metadata"
)

const (
	registerModeAppend = "append"
	signTimeHeader     = "x-md-sign-time"
	signTokenHeader    = "x-md-sign-token"
)

// Register 启动时向 manager 自注册菜单与接口权限（app="gateway"）
// 失败仅告警不阻断启动：菜单注册是增强能力，不影响网关服务流量
func Register(managerAddr, ak, sk string) {
	if managerAddr == "" {
		log.Warnw("msg", "manager addr is empty, skip register menu")
		return
	}
	conn, err := grpc.DialInsecure(context.Background(), grpc.WithEndpoint(managerAddr))
	if err != nil {
		log.Warnw("msg", "dial manager error, skip register menu", "err", err.Error())
		return
	}
	req := &managerMenu.RegisterMenuRequest{
		AppKeyword: appKeyword,
		Mode:       registerModeAppend,
		Menus:      menus,
	}

	ctx := context.Background()
	if ak != "" {
		// 与 kratosx library/signature 一致的签名算法：HMAC-SHA256("HMAC-SHA256\n{ts}\n{sha256(body)}", sk)
		body, _ := json.Marshal(req)
		ts := time.Now().Unix()
		hash := sha256.Sum256(body)
		content := fmt.Sprintf("HMAC-SHA256\n%d\n%s", ts, hex.EncodeToString(hash[:]))
		mac := hmac.New(sha256.New, []byte(sk))
		mac.Write([]byte(content))
		ctx = metadata.AppendToOutgoingContext(ctx,
			signTimeHeader, strconv.FormatInt(ts, 10),
			signTokenHeader, hex.EncodeToString(mac.Sum(nil)),
		)
	}

	reply, err := managerMenu.NewMenuClient(conn).RegisterMenu(ctx, req)
	if err != nil {
		log.Warnw("msg", "register menu error", "err", err.Error())
		return
	}
	log.Infow("msg", "register menu success",
		"created", reply.Created,
		"updated", reply.Updated,
		"deleted", reply.Deleted,
	)
}
