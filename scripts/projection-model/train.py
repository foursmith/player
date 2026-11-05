"""Train a small seven-class prototype and export a float16-weight TFLite model."""

import argparse
import hashlib
import json
import os
import time
from pathlib import Path

os.environ.setdefault("TF_CPP_MIN_LOG_LEVEL", "2")
os.environ.setdefault("TF_NUM_INTRAOP_THREADS", "4")
os.environ.setdefault("TF_NUM_INTEROP_THREADS", "2")
os.environ.setdefault("KERAS_HOME", str(Path(".cache/projection-model/keras").resolve()))

import numpy as np
import tensorflow as tf

from dataset import LABELS, SIZE, scene_group


def metrics(labels, probabilities):
    predictions = probabilities.argmax(axis=1)
    confusion = np.zeros((7, 7), dtype=int)
    for target, predicted in zip(labels, predictions):
        confusion[target, predicted] += 1
    return {"accuracy": float((predictions == labels).mean()), "confusion": confusion.tolist(), "per_class_accuracy": {name: float((predictions[labels == i] == i).mean()) for i, name in enumerate(LABELS)}}


def main(args):
    tf.keras.utils.set_random_seed(args.seed)
    args.out.mkdir(parents=True, exist_ok=True)
    splits = {name: np.load(args.data / (name + ".npz")) for name in ("train", "validation", "test")}
    groups = {name: {scene_group(str(group)) for group in data["groups"]} for name, data in splits.items()}
    assert not (groups["train"] & groups["validation"] or groups["train"] & groups["test"] or groups["test"] & groups["validation"]), "Source scene leaked between splits"

    base = tf.keras.applications.MobileNetV3Small(input_shape=(SIZE, SIZE, 3), include_top=False, weights="imagenet", include_preprocessing=True)
    base.trainable = False
    extractor = tf.keras.Sequential([base, tf.keras.layers.AveragePooling2D(2), tf.keras.layers.Flatten()], name="projection_features")
    features = {}
    for name, data in splits.items():
        cache = args.out / (name + "_features.npy")
        cache_info = args.out / (name + "_features.json")
        with (args.data / (name + ".npz")).open("rb") as source:
            digest = hashlib.file_digest(source, "sha256").hexdigest()
        fingerprint = {"dataset_sha256": digest, "tensorflow": tf.__version__, "keras": tf.keras.__version__, "extractor": "MobileNetV3Small-ImageNet-224-AveragePooling2D-2-v1"}
        if cache.exists() and cache_info.exists() and json.loads(cache_info.read_text()) == fingerprint:
            features[name] = np.load(cache)
        else:
            began = time.perf_counter()
            features[name] = extractor.predict(data["images"].astype(np.float32), batch_size=32, verbose=0)
            np.save(cache, features[name])
            cache_info.write_text(json.dumps(fingerprint, indent=2) + "\n")
            print(name, "features", features[name].shape, "seconds", round(time.perf_counter() - began, 2), flush=True)
    center, spread = float(features["train"].mean()), float(features["train"].std())
    head = tf.keras.Sequential([
        tf.keras.Input(shape=features["train"].shape[1:]),
        tf.keras.layers.Rescaling(1 / max(spread, .0001), offset=-center / max(spread, .0001)),
        tf.keras.layers.Dense(128, activation="relu", kernel_regularizer=tf.keras.regularizers.L2(.0001)),
        tf.keras.layers.Dropout(.35),
        tf.keras.layers.Dense(len(LABELS), activation="softmax"),
    ], name="projection_head")
    head.compile(optimizer=tf.keras.optimizers.Adam(.0005), loss="sparse_categorical_crossentropy", metrics=["accuracy"])
    head.fit(features["train"], splits["train"]["labels"], validation_data=(features["validation"], splits["validation"]["labels"]), epochs=args.epochs, batch_size=32, verbose=2, callbacks=[tf.keras.callbacks.EarlyStopping(monitor="val_loss", patience=10, restore_best_weights=True)])

    report = {"labels": LABELS, "seed": args.seed, "training": "ImageNet MobileNetV3-Small frozen backbone; projection head trained on synthetic projections of CC0 panoramas", "splits": {name: {"scenes": len(groups[name]), "frames": len(data["labels"])} for name, data in splits.items()}}
    inputs = tf.keras.Input(shape=(SIZE, SIZE, 3), dtype=tf.float32, name="rgb")
    model = tf.keras.Model(inputs, head(extractor(inputs, training=False), training=False))
    if args.fine_tune_epochs:
        base.trainable = True
        for layer in base.layers[:-50]:
            layer.trainable = False
        for layer in base.layers:
            if isinstance(layer, tf.keras.layers.BatchNormalization):
                layer.trainable = False
        model.compile(optimizer=tf.keras.optimizers.Adam(.00002), loss="sparse_categorical_crossentropy", metrics=["accuracy"])
        model.fit(splits["train"]["images"], splits["train"]["labels"], validation_data=(splits["validation"]["images"], splits["validation"]["labels"]), epochs=args.fine_tune_epochs, batch_size=24, verbose=2, callbacks=[tf.keras.callbacks.EarlyStopping(monitor="val_loss", patience=3, restore_best_weights=True)])
        report["training"] += f"; last 50 backbone layers fine-tuned for up to {args.fine_tune_epochs} epochs with frozen batch normalization"
    for name in ("validation", "test"):
        probabilities = model.predict(splits[name]["images"], batch_size=32, verbose=0)
        report[name] = metrics(splits[name]["labels"], probabilities)
    print(json.dumps(report, indent=2), flush=True)
    model.save(args.out / "projection.keras")
    # Export through SavedModel so the training-only optimizer and dropout do
    # not become part of the inference graph.
    model.export(str(args.out / "saved_model"), verbose=False)
    converter = tf.lite.TFLiteConverter.from_saved_model(str(args.out / "saved_model"))
    converter.optimizations = [tf.lite.Optimize.DEFAULT]
    converter.target_spec.supported_types = [tf.float16]
    converted = converter.convert()
    destination = args.out / "projection.tflite"
    destination.write_bytes(converted)
    report["model"] = {"sha256": hashlib.sha256(converted).hexdigest(), "bytes": len(converted), "parameters": model.count_params(), "input": [1, SIZE, SIZE, 3], "normalization": "RGB float32 0..255, complete frame aspect-fit on black, bilinear resize", "output": "softmax in labels order", "experimental": True}

    runtime = tf.lite.Interpreter(model_content=converted, num_threads=2)
    runtime.allocate_tensors()
    tensor_in, tensor_out = runtime.get_input_details()[0], runtime.get_output_details()[0]
    probabilities, timings = [], []
    for image in splits["test"]["images"]:
        runtime.set_tensor(tensor_in["index"], image[None].astype(np.float32))
        began = time.perf_counter()
        runtime.invoke()
        timings.append((time.perf_counter() - began) * 1000)
        probabilities.append(runtime.get_tensor(tensor_out["index"])[0])
    report["tflite_test"] = metrics(splits["test"]["labels"], np.asarray(probabilities))
    report["tflite_latency_ms"] = {"median": float(np.median(timings)), "p95": float(np.percentile(timings, 95))}
    (args.out / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    print("TFLite", len(converted), "bytes", "test accuracy", report["tflite_test"]["accuracy"], "latency", report["tflite_latency_ms"], flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, default=Path(".cache/projection-model/data"))
    parser.add_argument("--out", type=Path, default=Path(".cache/projection-model/experiment"))
    parser.add_argument("--epochs", type=int, default=60)
    parser.add_argument("--fine-tune-epochs", type=int, default=6)
    parser.add_argument("--seed", type=int, default=20261105)
    main(parser.parse_args())
