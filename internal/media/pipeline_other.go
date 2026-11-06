//go:build !darwin

package media

import "context"

func newVideoPipeline(*avStream, *avBuffer, context.Context) videoPipeline { return nil }
